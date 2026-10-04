package monitor_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/classifier"
	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/storage/sqlite"
)

type observer struct{}

func (observer) Source(context.Context, domain.SourceInfo, time.Duration, int, int, int, error) {}
func (observer) Cycle(context.Context, monitor.Run)                                             {}
func (observer) Delivery(context.Context, string)                                               {}

type fakeSource struct {
	name  string
	fail  bool
	calls atomic.Int64
}

func (f *fakeSource) Info() domain.SourceInfo {
	return domain.SourceInfo{Name: f.name, Kind: domain.FirstParty, ResetContext: true}
}
func (f *fakeSource) Fetch(ctx context.Context, _ time.Time) ([]domain.Item, error) {
	f.calls.Add(1)
	if f.fail {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}

type notifier struct {
	calls int
	fail  error
}

func (n *notifier) Send(context.Context, monitor.Notification) (int64, error) {
	n.calls++
	if n.fail != nil {
		err := n.fail
		n.fail = nil
		return 0, err
	}
	return 42, nil
}

func TestSourceFailureDoesNotBlockOthers(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	good, bad := &fakeSource{name: "good"}, &fakeSource{name: "bad", fail: true}
	service := monitor.Service{Store: store, Classifier: classifier.Rules{}, Sources: []monitor.Source{bad, good}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Observer: observer{}, Now: time.Now, Options: monitor.Options{Timeout: 20 * time.Millisecond, Lookback: time.Hour, MaxAge: 72 * time.Hour, Concurrency: 2, FailureThreshold: 3}}
	if err := service.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := store.SourceState(context.Background(), "good")
	if err != nil || !state.Initialized || good.calls.Load() != 1 {
		t.Fatal("healthy source blocked")
	}
	state, err = store.SourceState(context.Background(), "bad")
	if err != nil || state.Failures != 1 {
		t.Fatal("source failure not persisted")
	}
}

func TestDeliveryRetryAndNoReplay(t *testing.T) {
	for _, failure := range []error{&monitor.DeliveryRejected{Code: 429, RetryAfter: time.Minute}, &monitor.DeliveryRejected{Code: 500}, monitor.AmbiguousDelivery{}} {
		t.Run(failure.Error(), func(t *testing.T) {
			store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "watcher.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
			info := domain.SourceInfo{Name: "tibo", Kind: domain.FirstParty, ResetContext: true}
			opts := monitor.RecordOptions{ChatID: "1", NotifySignals: true, MaxAge: 72 * time.Hour}
			if _, err := store.CommitSource(context.Background(), info, nil, now, opts); err != nil {
				t.Fatal(err)
			}
			item := domain.Item{Source: info, ExternalID: "one", Text: "Codex reset tomorrow", PublishedAt: now}
			c, _ := (classifier.Rules{}).Classify(context.Background(), item)
			if _, err := store.CommitSource(context.Background(), info, []domain.ClassifiedItem{{Item: item, Classification: c}}, now, opts); err != nil {
				t.Fatal(err)
			}
			transport := &notifier{fail: failure}
			service := monitor.Service{Store: store, Notifier: transport, Observer: observer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
			if err := service.Deliver(context.Background()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Hour)
			if err := service.Deliver(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := service.Deliver(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 2
			var ambiguous monitor.AmbiguousDelivery
			if errors.As(failure, &ambiguous) {
				want = 1
			}
			if transport.calls != want {
				t.Fatalf("calls=%d want=%d", transport.calls, want)
			}
		})
	}
}
