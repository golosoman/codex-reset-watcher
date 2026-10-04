package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/classifier"
	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/source"
)

func TestProvenanceAndRolloutLifecycle(t *testing.T) {
	s, _, now := fixture(t)
	for _, name := range []string{"feed", "timeline", "reddit"} {
		initialize(t, s, name, now)
	}
	add := func(name, id, text, origin string, kind domain.SourceKind) []domain.Event {
		t.Helper()
		item := domain.Item{Source: domain.SourceInfo{Name: name, Kind: kind, ResetContext: true}, ExternalID: id, Text: text, PublishedAt: now, URL: "https://example.com/" + id, CanonicalOriginID: origin}
		if kind == domain.FirstPartyDerived {
			item.CanonicalAuthor = "thsottiaux"
		}
		item.Observations = source.Observations(item)
		c, err := (classifier.Rules{}).Classify(context.Background(), item)
		if err != nil {
			t.Fatal(err)
		}
		return commit(t, s, domain.ClassifiedItem{Item: item, Classification: c}, now)
	}
	announced := add("feed", "1", "Global Codex reset tomorrow", "1", domain.FirstPartyDerived)
	if len(announced) != 1 {
		t.Fatal("announcement missing")
	}
	if len(add("timeline", "copy", "Codex reset tomorrow", "1", domain.Aggregator)) != 0 {
		t.Fatal("origin counted twice")
	}
	now = now.Add(time.Hour)
	propagating := add("reddit", "2", "Codex Plus got it: 2% → 100%", "", domain.Community)
	if len(propagating) != 1 || propagating[0].Type != domain.ResetPropagating || propagating[0].GroupID != announced[0].GroupID {
		t.Fatalf("rollout fragmented: %+v", propagating)
	}
	if len(add("reddit", "3", "Codex Pro 5x still waiting, no reset yet", "", domain.Community)) != 0 {
		t.Fatal("negative observation notified")
	}
	now = now.Add(time.Hour)
	completed := add("feed", "4", "Reset all propagated.", "4", domain.FirstPartyDerived)
	if len(completed) != 1 || completed[0].GroupID != announced[0].GroupID {
		t.Fatal("completion fragmented")
	}
	if len(add("timeline", "old-origin-copy", "Codex reset tomorrow", "1", domain.Aggregator)) != 0 {
		t.Fatal("earlier canonical origin lost")
	}
	debug, err := s.EventDebug(context.Background(), completed[0].ID)
	if err != nil || len(debug.Evidence) != 6 || len(debug.Notifications) != 3 {
		t.Fatalf("debug=%+v error=%v", debug, err)
	}
}

func TestBaselineGraceAndStalePosts(t *testing.T) {
	for _, age := range []time.Duration{2 * time.Minute, time.Hour, 24 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			s, _, now := fixture(t)
			item := eventItem(t, "new-source", "one", "Codex reset tomorrow", now.Add(-age))
			_, err := s.CommitSource(context.Background(), item.Item.Source, []domain.ClassifiedItem{item}, now, monitor.RecordOptions{ChatID: "1", MaxAge: 2 * time.Hour, InitialNotifyWindow: 20 * time.Minute, NotifySignals: true})
			if err != nil {
				t.Fatal(err)
			}
			n, err := s.Pending(context.Background(), now)
			if err != nil || (n != nil) != (age < 20*time.Minute) {
				t.Fatalf("baseline: %+v %v", n, err)
			}
		})
	}
}

func TestMetadataChangeIsNotDiscardedAsDuplicate(t *testing.T) {
	s, _, now := fixture(t)
	initialize(t, s, "tracker", now)
	item := domain.Item{Source: domain.SourceInfo{Name: "tracker", Kind: domain.Aggregator, ResetContext: true}, ExternalID: "one", CanonicalOriginID: "123", Text: "Codex global reset tomorrow", PublishedAt: now}
	classify := func(item domain.Item) domain.ClassifiedItem {
		t.Helper()
		c, err := (classifier.Rules{}).Classify(context.Background(), item)
		if err != nil {
			t.Fatal(err)
		}
		return domain.ClassifiedItem{Item: item, Classification: c}
	}
	first := commit(t, s, classify(item), now)
	item.SourceFetchedAt = now.Add(time.Minute)
	if seen, err := s.Seen(context.Background(), item); err != nil || !seen {
		t.Fatal("fresh fetch caused a new version")
	}
	item.Observations = []domain.AccountObservation{{Plan: "plus", Status: "received", ObservedAt: now, SourceURL: item.URL}}
	if seen, err := s.Seen(context.Background(), item); err != nil || seen {
		t.Fatal("meaningful metadata change lost")
	}
	second := commit(t, s, classify(item), now)
	if len(first) != 1 || len(second) != 1 || second[0].Type != domain.ResetPropagating || first[0].GroupID != second[0].GroupID {
		t.Fatal("metadata transition failed")
	}
}

func TestMigrationFromProductionV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{string(body), "CREATE TABLE schema_migrations(version TEXT PRIMARY KEY)", "INSERT INTO schema_migrations VALUES('001_initial.sql')", `INSERT INTO reset_groups VALUES('old','global',5,1780000000,'old','old','https://example.com','Reset all propagated','hash')`, `INSERT INTO events VALUES('event','old','reset_completed','{}',1780000000)`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var rank, count int
	if err := s.db.QueryRow("SELECT rank FROM reset_groups WHERE id='old'").Scan(&rank); err != nil || rank != 6 {
		t.Fatalf("old rank=%d error=%v", rank, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil || count != 1 {
		t.Fatal("history lost")
	}
	if err := s.SetSourceHealth(context.Background(), "feed", "stale", time.Now()); err != nil {
		t.Fatal(err)
	}
	state, err := s.SourceState(context.Background(), "feed")
	if err != nil || state.Health != "stale" || state.LastChecked.IsZero() {
		t.Fatal("health not persisted")
	}
}
