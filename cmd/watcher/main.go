package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/golosoman/codex-reset-watcher/internal/classifier"
	"github.com/golosoman/codex-reset-watcher/internal/config"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/notifier/telegram"
	"github.com/golosoman/codex-reset-watcher/internal/observability"
	"github.com/golosoman/codex-reset-watcher/internal/scheduler"
	"github.com/golosoman/codex-reset-watcher/internal/source"
	"github.com/golosoman/codex-reset-watcher/internal/source/twiscan"
	"github.com/golosoman/codex-reset-watcher/internal/storage/sqlite"
	"github.com/golosoman/codex-reset-watcher/internal/translation"
)

func main() {
	if err := run(); err != nil {
		slog.Error("watcher stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		addr := os.Getenv("HTTP_LISTEN_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return errors.New("invalid HTTP_LISTEN_ADDR")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/readyz", nil)
		if err != nil {
			return err
		}
		res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			return errors.New("healthcheck transport failed")
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 {
			return errors.New("watcher not ready")
		}
		return nil
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return errors.New("invalid LOG_LEVEL")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	notifier := telegram.Client{HTTP: &http.Client{Timeout: 15 * time.Second}, Token: cfg.TelegramToken}
	if len(os.Args) > 1 && os.Args[1] == "validate-telegram" {
		return notifier.Validate(ctx)
	}
	store, err := sqlite.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer store.Close() //nolint:errcheck
	notifier.Translator = &translation.MyMemory{HTTP: &http.Client{Timeout: 4 * time.Second}, Cache: store}
	notifier.Logger = logger
	telemetry, err := observability.New(ctx, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Close(shutdown); err != nil {
			logger.Error("telemetry shutdown", "error", err)
		}
	}()
	client := httpio.New(15*time.Second, 4*1024*1024, telemetry.Tracer)
	sources := []monitor.Source{
		source.Optional{Source: source.CodexReset{Client: client, URL: cfg.FeedURL, MaxStaleness: cfg.SourceStaleAfter}, Enabled: cfg.FeedEnabled},
		source.Optional{Source: source.CodexReset{Client: client, URL: cfg.TimelineURL, Timeline: true, MaxStaleness: cfg.SourceStaleAfter}, Enabled: cfg.TimelineEnabled},
		source.Fallback{Optional: source.Optional{Source: twiscan.Source{Client: client, URL: cfg.TwiscanURL}, Enabled: cfg.TwiscanEnabled}, Primary: "codex-reset-feed"},
		source.Fallback{Optional: source.Optional{Source: source.Feed{Client: client, Name: "rsshub-tibo", URL: cfg.RSSHubURL, Format: "rss", Derived: true}, Enabled: cfg.RSSHubEnabled}, Primary: "twiscan-tibo"},
		source.Optional{Source: source.Feed{Client: client, Name: "openai-community", URL: cfg.CommunityURL, Format: "rss"}, Enabled: cfg.CommunityEnabled},
		source.Optional{Source: source.Feed{Client: client, Name: "reddit-codex", URL: cfg.RedditURL, Format: "rss"}, Enabled: cfg.RedditEnabled},
		source.Status{Client: client}, source.Documents{Client: client, URLs: cfg.HelpURLs},
	}
	sources = append(sources, source.Documents{Client: client, Name: "openai-docs", URLs: []string{"https://learn.chatgpt.com/docs/pricing.md"}})
	for _, feed := range cfg.Feeds {
		sources = append(sources, source.Feed{Client: client, Name: feed.Name, URL: feed.URL, Format: feed.Format})
	}
	composite := classifier.Composite{Rules: classifier.Rules{}, Cache: store, Logger: logger}
	if cfg.LLMEnabled {
		composite.Model = &classifier.LLM{HTTP: &http.Client{Timeout: 15 * time.Second}, Key: cfg.LLMKey, Model: cfg.LLMModel}
	}
	service := &monitor.Service{Store: store, Classifier: composite, Notifier: notifier, Sources: sources, Logger: logger, Observer: telemetry, Tracer: telemetry.Tracer, Now: func() time.Time { return time.Now().UTC() }, Options: monitor.Options{Timeout: cfg.SourceTimeout, Lookback: cfg.Lookback, MaxAge: cfg.MaxAge, Concurrency: cfg.Concurrency, FailureThreshold: cfg.FailureThreshold, NotifySignals: cfg.NotifySignals, ChatID: cfg.ChatID, AdminChatID: cfg.AdminChatID, InitialNotifyWindow: cfg.InitialNotifyWindow, SuppressPropagating: !cfg.NotifyPropagating}}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", telemetry.Handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"alive"}`)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.Ping(pingCtx); err != nil {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"status":"storage_unavailable"}`)
			return
		}
		states := map[string]monitor.SourceState{}
		for _, adapter := range sources {
			state, err := store.SourceState(pingCtx, adapter.Info().Name)
			if err != nil {
				w.WriteHeader(503)
				return
			}
			states[adapter.Info().Name] = state
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"status": "ready", "sources": states}); err != nil {
			logger.Warn("encode readiness", "error", err)
		}
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		states := map[string]monitor.SourceState{}
		for _, adapter := range sources {
			state, err := store.SourceState(r.Context(), adapter.Info().Name)
			if err != nil {
				http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
				return
			}
			states[adapter.Info().Name] = state
		}
		history, err := store.History(r.Context())
		if err != nil {
			http.Error(w, "history unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"sources": states, "recent_events": history}); err != nil {
			logger.Warn("encode status", "error", err)
		}
	})
	mux.HandleFunc("GET /history", func(w http.ResponseWriter, r *http.Request) {
		history, err := store.History(r.Context())
		if err != nil {
			http.Error(w, "history unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(history); err != nil {
			logger.Warn("encode history", "error", err)
		}
	})
	mux.HandleFunc("GET /events/{id}", func(w http.ResponseWriter, r *http.Request) {
		debug, err := store.EventDebug(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "event not found or unavailable", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(debug); err != nil {
			logger.Warn("encode evidence", "error", err)
		}
	})
	server := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	var workers sync.WaitGroup
	onError := func(err error) { logger.ErrorContext(ctx, "background task failed", "error", err) }
	workers.Go(func() { scheduler.Run(ctx, cfg.Interval, cfg.Jitter, service.Check, onError) })
	workers.Go(func() { scheduler.Run(ctx, 30*time.Second, 0, service.Deliver, onError) })
	logger.Info("watcher started", "check_interval", cfg.Interval.String(), "sources", len(sources), "llm_enabled", cfg.LLMEnabled)
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
			workers.Wait()
			return fmt.Errorf("HTTP server: %w", err)
		}
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = server.Shutdown(shutdown)
	workers.Wait()
	return err
}
