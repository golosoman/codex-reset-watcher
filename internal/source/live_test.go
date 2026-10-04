package source_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/source"
	"github.com/golosoman/codex-reset-watcher/internal/source/twiscan"
)

func TestPublicSourcesLive(t *testing.T) {
	if os.Getenv("WATCHER_LIVE_TEST") != "1" {
		t.Skip("opt-in live read-only probe")
	}
	client := httpio.New(15*time.Second, 4*1024*1024, nil)
	for _, adapter := range []monitor.Source{
		source.CodexReset{Client: client, URL: "https://codex-reset.com/api/feed"},
		source.CodexReset{Client: client, URL: "https://codex-reset.com/api/timeline", Timeline: true},
		source.Feed{Client: client, Name: "community", URL: "https://community.openai.com/latest.rss", Format: "rss"},
		twiscan.Source{Client: client, URL: "https://twiscan.com/en/x/thsottiaux"},
	} {
		t.Run(adapter.Info().Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			items, err := adapter.Fetch(ctx, time.Time{})
			if err != nil || len(items) == 0 {
				t.Fatalf("items=%d error=%v", len(items), err)
			}
			t.Logf("parsed %d publications", len(items))
		})
	}
}
