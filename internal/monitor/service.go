package monitor

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"go.opentelemetry.io/otel/trace"
)

type Options struct {
	Timeout, Lookback, MaxAge     time.Duration
	Concurrency, FailureThreshold int
	NotifySignals                 bool
	ChatID, AdminChatID           string
	InitialNotifyWindow           time.Duration
	SuppressPropagating           bool
}
type Service struct {
	Store               Store
	Classifier          Classifier
	Notifier            Notifier
	Sources             []Source
	Logger              *slog.Logger
	Observer            Observer
	Tracer              trace.Tracer
	Options             Options
	Now                 func() time.Time
	checkMu, deliveryMu sync.Mutex
}

func (s *Service) Check(ctx context.Context) error {
	if !s.checkMu.TryLock() {
		return nil
	}
	defer s.checkMu.Unlock()
	if s.Tracer != nil {
		var span trace.Span
		ctx, span = s.Tracer.Start(ctx, "watcher.check")
		defer span.End()
	}
	start := s.Now()
	id := rand.Text()
	logger := s.Logger.With("check_id", id)
	var failures, created atomic.Int64
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, max(1, s.Options.Concurrency))
	// Complete primary checks before deciding whether their fallbacks are needed.
	for phase := range 3 {
		for _, adapter := range s.Sources {
			fallback, isFallback := adapter.(interface{ FallbackFor() string })
			adapterPhase := 0
			if isFallback {
				adapterPhase = 1
				if fallback.FallbackFor() != "codex-reset-feed" {
					adapterPhase = 2
				}
			}
			if phase != adapterPhase {
				continue
			}
			wg.Go(func() {
				select {
				case semaphore <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-semaphore }()
				began := s.Now()
				info := adapter.Info()
				if configured, ok := adapter.(interface{ Active() bool }); ok && !configured.Active() {
					s.setHealth(ctx, info.Name, "disabled")
					return
				}
				if isFallback {
					primary, err := s.Store.SourceState(ctx, fallback.FallbackFor())
					if err == nil && (primary.Health == "healthy" || primary.Health == "standby") && s.Now().Sub(primary.LastChecked) < 15*time.Minute {
						s.setHealth(ctx, info.Name, "standby")
						return
					}
				}
				items, newItems, newEvents, err := s.checkSource(ctx, adapter)
				if errors.Is(err, ErrSourceDisabled) {
					s.setHealth(ctx, info.Name, "disabled")
					return
				}
				s.Observer.Source(ctx, info, s.Now().Sub(began), items, newItems, len(newEvents), err)
				if err != nil {
					health := "unavailable"
					var problem *SourceProblem
					if errors.As(err, &problem) {
						health = problem.Health
					}
					s.setHealth(ctx, info.Name, health)
					failures.Add(1)
					logger.WarnContext(ctx, "source check failed", "source", info.Name, "error", err)
					admin := s.Options.AdminChatID
					if errors.Is(err, ErrSourceUnavailable) {
						admin = ""
					}
					if saveErr := s.Store.SourceFailure(ctx, info.Name, err.Error(), s.Now(), s.Options.FailureThreshold, admin); saveErr != nil {
						logger.ErrorContext(ctx, "persist source failure", "source", info.Name, "error", saveErr)
					}
					return
				}
				s.setHealth(ctx, info.Name, "healthy")
				created.Add(int64(len(newEvents)))
				if observer, ok := s.Observer.(interface {
					Event(context.Context, domain.Event)
				}); ok {
					for _, event := range newEvents {
						observer.Event(ctx, event)
					}
				}
				logger.InfoContext(ctx, "source checked", "source", info.Name, "duration_ms", s.Now().Sub(began).Milliseconds(), "items_received", items, "items_new", newItems, "events_created", len(newEvents))
			})
		}
		wg.Wait()
	}
	run := Run{ID: id, Started: start, Duration: s.Now().Sub(start), Failures: int(failures.Load()), Events: int(created.Load())}
	s.Observer.Cycle(ctx, run)
	if err := s.Store.SaveRun(ctx, run); err != nil {
		return fmt.Errorf("persist check: %w", err)
	}
	logger.InfoContext(ctx, "check completed", "source_failures", run.Failures, "events_created", run.Events, "duration_ms", run.Duration.Milliseconds())
	return ctx.Err()
}

func (s *Service) setHealth(ctx context.Context, name, health string) {
	if observer, ok := s.Observer.(interface{ Health(string, string) }); ok {
		observer.Health(name, health)
	}
	if store, ok := s.Store.(HealthStore); ok {
		if err := store.SetSourceHealth(ctx, name, health, s.Now()); err != nil {
			s.Logger.Error("persist source health", "source", name, "error", err)
		}
	}
}

func (s *Service) checkSource(ctx context.Context, adapter Source) (int, int, []domain.Event, error) {
	child, cancel := context.WithTimeout(ctx, s.Options.Timeout)
	defer cancel()
	info := adapter.Info()
	state, err := s.Store.SourceState(child, info.Name)
	if err != nil {
		return 0, 0, nil, err
	}
	since := s.Now().Add(-s.Options.Lookback)
	if state.Initialized {
		since = s.Now().Add(-s.Options.MaxAge)
	}
	items, err := adapter.Fetch(child, since)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(items) > 1000 {
		return len(items), 0, nil, errors.New("source item count exceeds limit")
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishedAt.Before(items[j].PublishedAt) })
	classified := make([]domain.ClassifiedItem, 0, len(items))
	for _, item := range items {
		if item.Source.Name == "" {
			item.Source = info
		}
		if item.Source.Name != info.Name {
			return len(items), 0, nil, errors.New("source item name mismatch")
		}
		if item.ExternalID == "" || len(item.Text) > 50000 {
			return len(items), len(classified), nil, errors.New("source returned invalid item")
		}
		seen, err := s.Store.Seen(child, item)
		if err != nil {
			return len(items), len(classified), nil, err
		}
		if seen {
			continue
		}
		classification, err := s.Classifier.Classify(child, item)
		if err != nil {
			return len(items), len(classified), nil, fmt.Errorf("classify publication: %w", err)
		}
		classified = append(classified, domain.ClassifiedItem{Item: item, Classification: classification})
	}
	events, err := s.Store.CommitSource(child, info, classified, s.Now(), RecordOptions{NotifySignals: s.Options.NotifySignals, ChatID: s.Options.ChatID, MaxAge: s.Options.MaxAge, InitialNotifyWindow: s.Options.InitialNotifyWindow, SuppressPropagating: s.Options.SuppressPropagating})
	return len(items), len(classified), events, err
}

func (s *Service) Deliver(ctx context.Context) error {
	if !s.deliveryMu.TryLock() {
		return nil
	}
	defer s.deliveryMu.Unlock()
	for range 50 {
		if err := ctx.Err(); err != nil {
			return err
		}
		delivery, err := s.Store.Pending(ctx, s.Now())
		if err != nil {
			return err
		}
		if delivery == nil {
			return nil
		}
		claimed, err := s.Store.Claim(ctx, delivery.ID, s.Now())
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		messageID, sendErr := s.Notifier.Send(callCtx, *delivery)
		cancel()
		state, reason, next := "sent", "", s.Now()
		if sendErr != nil {
			reason = sendErr.Error()
			state = "pending"
			delay := time.Second * time.Duration(1<<min(delivery.Attempts, 8))
			next = next.Add(delay + time.Duration(mathrand.Int64N(int64(delay/2)+1)))
			var ambiguous AmbiguousDelivery
			var rejected *DeliveryRejected
			if errors.As(sendErr, &ambiguous) {
				state = "uncertain"
			} else if errors.As(sendErr, &rejected) {
				next = maxTime(next, s.Now().Add(rejected.RetryAfter))
				if rejected.Code != 429 && rejected.Code < 500 {
					state = "failed"
				}
			}
			if delivery.Attempts >= 9 {
				state = "failed"
			}
		}
		// Persist the observable outcome even while the process is shutting down.
		saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		saveErr := s.Store.Finish(saveCtx, delivery.ID, state, messageID, reason, next)
		cancelSave()
		if saveErr != nil {
			return fmt.Errorf("persist delivery outcome: %w", saveErr)
		}
		s.Observer.Delivery(ctx, state)
		s.Logger.InfoContext(ctx, "notification delivery", "notification_id", delivery.ID, "status", state, "error", sendErr)
	}
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
