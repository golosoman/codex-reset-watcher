package monitor_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/classifier"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/source"
	"github.com/golosoman/codex-reset-watcher/internal/storage/sqlite"
)

func TestFallbackUsesCurrentCycleHealth(t *testing.T) {
	for _, failure := range []bool{false, true} {
		s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "watcher.db"))
		if err != nil {
			t.Fatal(err)
		}
		primary := &fakeSource{name: "codex-reset-feed", fail: failure}
		fallback := &fakeSource{name: "fallback"}
		service := monitor.Service{Store: s, Classifier: classifier.Rules{}, Sources: []monitor.Source{source.Fallback{Optional: source.Optional{Source: fallback, Enabled: true}, Primary: "codex-reset-feed"}, primary}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Observer: observer{}, Now: time.Now, Options: monitor.Options{Timeout: 10 * time.Millisecond, MaxAge: time.Hour, Lookback: time.Hour, Concurrency: 2}}
		if err := service.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if failure {
			want = 1
		}
		if fallback.calls.Load() != want {
			t.Fatalf("failure=%v fallback calls=%d", failure, fallback.calls.Load())
		}
		_ = s.Close()
	}
}
