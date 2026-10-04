package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/classifier"
	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

func fixture(t *testing.T) (*Store, string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watcher.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path, time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
}
func info(name string) domain.SourceInfo {
	return domain.SourceInfo{Name: name, Kind: domain.FirstParty, ResetContext: true}
}
func eventItem(t *testing.T, name, id, text string, stamp time.Time) domain.ClassifiedItem {
	t.Helper()
	item := domain.Item{Source: info(name), ExternalID: id, Text: text, URL: "https://example.com/" + id, PublishedAt: stamp}
	c, err := (classifier.Rules{}).Classify(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ClassifiedItem{Item: item, Classification: c}
}
func initialize(t *testing.T, s *Store, name string, stamp time.Time) {
	t.Helper()
	if _, err := s.CommitSource(context.Background(), info(name), nil, stamp, monitor.RecordOptions{ChatID: "1", MaxAge: 72 * time.Hour, NotifySignals: true}); err != nil {
		t.Fatal(err)
	}
}
func commit(t *testing.T, s *Store, item domain.ClassifiedItem, stamp time.Time) []domain.Event {
	t.Helper()
	events, err := s.CommitSource(context.Background(), item.Item.Source, []domain.ClassifiedItem{item}, stamp, monitor.RecordOptions{ChatID: "1", MaxAge: 72 * time.Hour, NotifySignals: true})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestBootstrapAndRestart(t *testing.T) {
	s, path, now := fixture(t)
	historical := eventItem(t, "tibo", "old", "Codex usage limits have been reset", now.Add(-time.Hour))
	commit(t, s, historical, now)
	if n, err := s.Pending(context.Background(), now); err != nil || n != nil {
		t.Fatalf("bootstrap notified: %+v %v", n, err)
	}
	item := eventItem(t, "tibo", "new", "We are loading a banked reset into all accounts", now)
	if len(commit(t, s, item, now)) != 1 {
		t.Fatal("new event missing")
	}
	if len(commit(t, s, item, now)) != 0 {
		t.Fatal("exact duplicate")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	n, err := reopened.Pending(context.Background(), now)
	if err != nil || n == nil {
		t.Fatalf("outbox lost: %v", err)
	}
	claimed, err := reopened.Claim(context.Background(), n.ID, now)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	if err := reopened.Finish(context.Background(), n.ID, "sent", 42, "", now); err != nil {
		t.Fatal(err)
	}
	if n, _ := reopened.Pending(context.Background(), now); n != nil {
		t.Fatal("sent message replayed")
	}
}

func TestLifecycleAndCrossSourceDuplicate(t *testing.T) {
	s, _, now := fixture(t)
	initialize(t, s, "tibo", now)
	initialize(t, s, "official", now)
	texts := []string{"Burn those tokens", "Codex reset tomorrow", "Usage limits have been reset for all paid users"}
	var group string
	for i, text := range texts {
		stamp := now.Add(time.Duration(i) * time.Hour)
		events := commit(t, s, eventItem(t, "tibo", text, text, stamp), stamp)
		if len(events) != 1 {
			t.Fatalf("transition missing: %s", text)
		}
		if i == 0 {
			group = events[0].GroupID
		}
		if events[0].GroupID != group {
			t.Fatal("lifecycle fragmented")
		}
	}
	last := texts[2]
	if events := commit(t, s, eventItem(t, "official", "repost", last, now.Add(2*time.Hour)), now.Add(2*time.Hour)); len(events) != 0 {
		t.Fatal("cross-source duplicate notified")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM notifications").Scan(&count); err != nil || count != 3 {
		t.Fatalf("notifications=%d error=%v", count, err)
	}
}

func TestSendingRecoveredAsUncertain(t *testing.T) {
	s, path, now := fixture(t)
	initialize(t, s, "tibo", now)
	commit(t, s, eventItem(t, "tibo", "one", "Codex reset tomorrow", now), now)
	n, _ := s.Pending(context.Background(), now)
	if n == nil {
		t.Fatal("missing delivery")
	}
	if ok, err := s.Claim(context.Background(), n.ID, now); err != nil || !ok {
		t.Fatal(err)
	}
	_ = s.Close()
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if n, _ := reopened.Pending(context.Background(), now); n != nil {
		t.Fatal("uncertain delivery replayed")
	}
	var state string
	if err := reopened.db.QueryRow("SELECT status FROM notifications").Scan(&state); err != nil || state != "uncertain" {
		t.Fatalf("state=%s error=%v", state, err)
	}
}

func TestBankedAndGlobalResetsStaySeparate(t *testing.T) {
	s, _, now := fixture(t)
	initialize(t, s, "tibo", now)
	global := commit(t, s, eventItem(t, "tibo", "global", "Codex usage limits have been reset", now), now)
	banked := commit(t, s, eventItem(t, "tibo", "banked", "We are loading a banked reset into all accounts", now), now)
	if len(global) != 1 || len(banked) != 1 || global[0].GroupID == banked[0].GroupID {
		t.Fatal("reset kinds merged")
	}
}
