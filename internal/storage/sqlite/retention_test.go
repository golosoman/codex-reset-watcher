package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/storage/sqlite"
)

func TestRetentionPreservesCorrelatedEvidence(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	info := domain.SourceInfo{Name: "official-docs", Kind: domain.Official}
	opts := monitor.RecordOptions{ChatID: "1", MaxAge: 72 * time.Hour, NotifySignals: true}
	first := domain.Item{Source: info, ExternalID: "first", Text: "Codex usage limits have been reset"}
	duplicate := first
	duplicate.ExternalID = "duplicate"
	irrelevant := domain.Item{Source: info, ExternalID: "policy", Text: "Codex pricing policy"}
	items := []domain.ClassifiedItem{
		{Item: first, Classification: domain.Classification{Type: domain.GlobalResetConfirmed, Scope: "global", Confidence: .95}},
		{Item: duplicate, Classification: domain.Classification{Type: domain.GlobalResetConfirmed, Scope: "global", Confidence: .95}},
		{Item: irrelevant, Classification: domain.Classification{Type: domain.NotRelevant}},
	}
	if _, err := store.CommitSource(ctx, info, items, now, opts); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, monitor.Run{ID: "retention", Started: now.Add(31 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	seen, err := store.Seen(ctx, duplicate)
	if err != nil || !seen {
		t.Fatalf("correlated evidence forgotten: seen=%v err=%v", seen, err)
	}
	seen, err = store.Seen(ctx, irrelevant)
	if err != nil || seen {
		t.Fatalf("irrelevant item not pruned: seen=%v err=%v", seen, err)
	}
}
