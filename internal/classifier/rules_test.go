package classifier

import (
	"context"
	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"testing"
)

func TestRules(t *testing.T) {
	cases := []struct {
		text string
		want domain.EventType
	}{
		{"Usage limits have been reset for all paid users", domain.GlobalResetConfirmed},
		{"We are loading a banked reset into all Plus, Pro and Business accounts", domain.BankedResetConfirmed},
		{"Reset tomorrow at 6 PM Pacific", domain.ResetAnnounced},
		{"Reset all propagated. Enjoy.", domain.ResetCompleted},
		{"All reset for everyone", domain.ResetCompleted},
		{"Burn those tokens", domain.ResetSignal},
		{"More resets coming next week", domain.ResetAnnounced},
		{"Resetting everyone", domain.GlobalResetConfirmed},
		{"Usage reset in the next hour", domain.ResetImminent},
		{"Reset your password", domain.NotRelevant},
		{"Your usage resets every week", domain.NotRelevant},
		{"No global reset tomorrow", domain.NotRelevant},
		{"A new Codex model is out", domain.NotRelevant},
		{"Reset", domain.NotRelevant},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			item := domain.Item{Text: tc.text, Source: domain.SourceInfo{Kind: domain.FirstParty, ResetContext: true}}
			got, err := (Rules{}).Classify(context.Background(), item)
			if err != nil || got.Type != tc.want {
				t.Fatalf("got %s (%v), want %s", got.Type, err, tc.want)
			}
		})
	}
}
func TestCommunityCannotConfirm(t *testing.T) {
	item := domain.Item{Text: "Codex usage limits have been reset for all paid users", Source: domain.SourceInfo{Kind: domain.Community}}
	got, err := (Rules{}).Classify(context.Background(), item)
	if err != nil || got.Type != domain.ResetSignal || got.Confidence > .55 {
		t.Fatalf("unsafe community classification: %+v %v", got, err)
	}
}
func TestUnscopedResetDoesNotTrigger(t *testing.T) {
	got, _ := (Rules{}).Classify(context.Background(), domain.Item{Text: "Reset tomorrow", Source: domain.SourceInfo{Kind: domain.Official}})
	if got.Type != domain.NotRelevant {
		t.Fatal(got)
	}
}
