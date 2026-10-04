package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

func TestCodexResetFixtures(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, timeline := range []bool{false, true} {
		file := "feed"
		if timeline {
			file = "timeline"
		}
		body, err := os.ReadFile("../../testdata/codex-reset-" + file + ".json")
		if err != nil {
			t.Fatal(err)
		}
		items, err := ParseCodexReset(body, timeline, (CodexReset{Timeline: timeline}).Info(), now, time.Hour)
		if err != nil || len(items) != 2 {
			t.Fatalf("items=%+v error=%v", items, err)
		}
		if !timeline {
			if items[0].Source.Kind != domain.FirstPartyDerived || items[0].CanonicalOriginID == "" || !items[1].IsReply || items[1].ReplyToID == "" {
				t.Fatal("provenance/reply lost")
			}
		} else if items[0].Source.Kind != domain.Aggregator || len(items[1].Observations) != 1 || items[1].Observations[0].Status != "banked_reset_seen" {
			t.Fatal("timeline trust or observation incorrect")
		}
	}
}

func TestCodexResetFreshnessAndInvalidData(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ body, health string }{
		{`{`, "degraded"}, {`{}`, "degraded"},
		{`{"fetched_at":"2026-10-04T12:00:00Z","tweets":[],"stale":true}`, "stale"},
		{`{"fetched_at":"2026-10-03T12:00:00Z","tweets":[]}`, "stale"},
		{`{"fetched_at":"2026-10-04T12:00:00Z","tweets":[{"id":"1","text":"Reset","at":"bad"}]}`, ""},
		{`{"fetched_at":"2026-10-04T12:00:00Z","tweets":[{"id":"1"}]}`, "degraded"},
	} {
		_, err := ParseCodexReset([]byte(tc.body), false, (CodexReset{}).Info(), now, time.Hour)
		if err == nil {
			t.Fatal("invalid response accepted")
		}
		var problem *monitor.SourceProblem
		if tc.health != "" && (!errors.As(err, &problem) || problem.Health != tc.health) {
			t.Fatalf("health: %v", err)
		}
	}
}

func TestCodexResetNoTrustEscalation(t *testing.T) {
	body, _ := os.ReadFile("../../testdata/codex-reset-feed.json")
	for _, changed := range []string{strings.ReplaceAll(string(body), "thsottiaux", "other"), strings.ReplaceAll(string(body), "x.com/", "x.com.evil/"), strings.ReplaceAll(string(body), `"handle":"thsottiaux"`, `"handle":"other"`)} {
		items, err := ParseCodexReset([]byte(changed), false, (CodexReset{}).Info(), time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if domain.Trusted(item.Source.Kind) {
				t.Fatal("spoofed author promoted")
			}
		}
	}
}

func TestCodexResetHTTPFailure(t *testing.T) {
	for _, code := range []int{403, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code); _, _ = io.WriteString(w, "failed") }))
		client := httpio.New(50*time.Millisecond, 4096, nil)
		client.Attempts = 1
		_, err := (CodexReset{Client: client, URL: server.URL}).Fetch(context.Background(), time.Time{})
		server.Close()
		if err == nil {
			t.Fatal("HTTP failure ignored")
		}
	}
}

func TestPlanObservations(t *testing.T) {
	for _, tc := range []struct{ text, plan, status string }{
		{"Codex Plus got it: 2% → 100%", "plus", "received"},
		{"Codex Pro 5x still waiting, no reset yet", "pro 5x", "not_received"},
		{"Pro 20x received a banked reset", "pro 20x", "banked_reset_seen"},
	} {
		observations := Observations(domain.Item{Text: tc.text})
		if len(observations) != 1 || observations[0].Plan != tc.plan || observations[0].Status != tc.status {
			t.Fatalf("%s: %+v", tc.text, observations)
		}
	}
}

func FuzzCanonicalOrigin(f *testing.F) {
	f.Add("https://x.com/thsottiaux/status/123")
	f.Add("https://x.com.evil/thsottiaux/status/123")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 65536 {
			return
		}
		author, id, canonical := CanonicalOrigin(raw)
		if canonical != "" && (author == "" || id == "") {
			t.Fatal("incomplete canonical origin")
		}
	})
}
func FuzzCodexReset(f *testing.F) {
	f.Add([]byte(`{"fetched_at":"2026-10-04T12:00:00Z","tweets":[]}`))
	f.Fuzz(func(_ *testing.T, body []byte) {
		if len(body) < 65536 {
			_, _ = ParseCodexReset(body, false, (CodexReset{}).Info(), time.Now(), time.Hour)
		}
	})
}
