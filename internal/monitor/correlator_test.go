package monitor

import (
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

func TestIdenticalAnnouncementsOnDifferentDaysAreSeparate(t *testing.T) {
	now := time.Now().UTC()
	item := domain.Item{Source: domain.SourceInfo{Name: "tibo"}, ExternalID: "new", Text: "Codex reset today", PublishedAt: now}
	group := Group{ID: "previous", Scope: "global", Source: "tibo", ExternalID: "old", Text: item.Text, Hash: domain.Hash(item.Text), Rank: 2, LatestAt: now.Add(-24 * time.Hour)}
	classification := domain.Classification{Type: domain.ResetAnnounced, Scope: "global"}
	if Correlate(item, classification, []Group{group}) != nil {
		t.Fatal("new daily announcement merged into yesterday's reset")
	}
	group.LatestAt = now.Add(-time.Hour)
	if Correlate(item, classification, []Group{group}) == nil {
		t.Fatal("nearby duplicate did not correlate")
	}
}
