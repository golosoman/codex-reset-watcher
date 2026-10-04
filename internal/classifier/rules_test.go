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
		{"Usage limits have been reset for all paid users today", domain.GlobalResetConfirmed},
		{"Codex usage has been reset a few minutes ago", domain.GlobalResetConfirmed},
		{"Banked resets have been reset today", domain.BankedResetConfirmed},
		{"Banked resets are available", domain.BankedResetConfirmed},
		{"Codex usage limits will be restored tomorrow", domain.ResetAnnounced},
		{"Codex usage limits have been restored", domain.GlobalResetConfirmed},
		{"Codex usage will have been reset by tonight", domain.ResetAnnounced},
		{"We are loading a banked reset into all Plus, Pro and Business accounts", domain.ResetAnnounced},
		{"Reset tomorrow at 6 PM Pacific", domain.ResetAnnounced},
		{"Reset all propagated. Enjoy.", domain.ResetCompleted},
		{"Codex reset all propagated will happen tomorrow", domain.ResetAnnounced},
		{"Pro 500 didn’t get the reset as expected earlier. Investigating and will make up for it", domain.NotRelevant},
		{"All reset for everyone", domain.ResetCompleted},
		{"Burn those tokens", domain.ResetSignal},
		{"More resets coming next week", domain.ResetSignal},
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

func TestDerivedTrustAndResetKinds(t *testing.T) {
	for _, tc := range []struct {
		text, scope string
		kind        domain.SourceKind
		want        domain.EventType
	}{
		{"Burn those tokens.", "unknown", domain.FirstPartyDerived, domain.ResetSignal},
		{"Who says it won't reset?", "unknown", domain.FirstPartyDerived, domain.ResetSignal},
		{"Global reset landing tomorrow 10am PST.", "global", domain.FirstPartyDerived, domain.ResetAnnounced},
		{"We are loading a banked reset.", "banked", domain.FirstPartyDerived, domain.ResetAnnounced},
		{"Codex reset is live.", "unknown", domain.FirstPartyDerived, domain.ResetConfirmed},
		{"Reset all propagated.", "global", domain.Aggregator, domain.ResetSignal},
		{"Connection reset by peer.", "unknown", domain.FirstPartyDerived, domain.NotRelevant},
	} {
		item := domain.Item{Text: tc.text, Source: domain.SourceInfo{Kind: tc.kind, ResetContext: true}}
		c, err := (Rules{}).Classify(context.Background(), item)
		if err != nil || c.Type != tc.want || c.Scope != tc.scope {
			t.Fatalf("%s: %+v %v", tc.text, c, err)
		}
	}
}
