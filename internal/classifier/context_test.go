package classifier

import (
	"context"
	"strings"
	"testing"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

func TestRedditDiscussionIsNotAResetAnnouncement(t *testing.T) {
	for _, text := range []string{
		"Considering moving to Claude Code: Objective views\nI use Codex for my website and personal apps. Tomorrow I will decide whether to renew my subscription. My allowance resets are inconsistent and not communicated. For some reason I found a banked reset in my account, so I used it. As expected, my reset changed to 7 days later, but then a day later it reset again to 100%.",
		"I'm too tired to keep trying :(\nAfter using a banked reset at around 10pm on the 1st October (and then the reset soon after), I've been trying to spend that 100% usage window before my 5th October banked reset expires. I'll have to use the banked reset before it expires even though I still have a lot of usage left.",
		"Did ChatGPT Scheduled Tasks recently start consuming your Codex/Work usage allowance?\nI have a normal ChatGPT Scheduled Task that runs every morning. Until today, I had never noticed it materially consuming my usage window. My allowance showed 29% used / 71% remaining. Does running a scheduled task reset the usage timer?",
		"Codex usage resets every week. Tomorrow I will use more tokens.",
		"Codex global reset tomorrow? I hope we get more usage soon.",
	} {
		t.Run(strings.Split(text, "\n")[0], func(t *testing.T) {
			c, err := (Rules{}).Classify(context.Background(), domain.Item{Text: text, Source: domain.SourceInfo{Kind: domain.Community}})
			if err != nil || c.Type != domain.NotRelevant {
				t.Fatalf("discussion sent as a reset: %+v error=%v", c, err)
			}
		})
	}
}

func TestLifecycleUsesOnlyRelatedSentence(t *testing.T) {
	for _, tc := range []struct {
		text, evidence, scope string
		want                  domain.EventType
	}{
		{"Tomorrow I will release a new Codex model. Usage limits have been reset for all paid users.", "Usage limits have been reset for all paid users.", "global", domain.GlobalResetConfirmed},
		{"A new Codex model is coming tomorrow. My banked reset changed to seven days later.", "", "unknown", domain.NotRelevant},
		{"Banked reset is a useful feature. A global reset is coming tomorrow.", "A global reset is coming tomorrow.", "global", domain.ResetAnnounced},
		{strings.Repeat("An unrelated Codex product update. ", 50) + "Usage limits will be restored tomorrow.", "Usage limits will be restored tomorrow.", "unknown", domain.ResetAnnounced},
		{"Reset your password before logging in. Codex usage limits have been reset for all paid users.", "Codex usage limits have been reset for all paid users.", "global", domain.GlobalResetConfirmed},
	} {
		c, err := (Rules{}).Classify(context.Background(), domain.Item{Text: tc.text, Source: domain.SourceInfo{Kind: domain.FirstPartyDerived}})
		if err != nil || c.Type != tc.want || c.Type != domain.NotRelevant && (c.Evidence != tc.evidence || c.Scope != tc.scope || !strings.Contains(tc.text, c.Evidence)) {
			t.Fatalf("incorrect evidence: %+v error=%v", c, err)
		}
	}
}

func TestCommunityPredictionsCannotBypassGuard(t *testing.T) {
	for _, eventType := range []domain.EventType{domain.ResetAnnounced, domain.ResetImminent, domain.ResetCompleted} {
		c := domain.Guard(domain.Item{Source: domain.SourceInfo{Kind: domain.Community}}, domain.Classification{Type: eventType, Confidence: .99, Scope: "global"})
		if c.Type != domain.NotRelevant || c.Notify(true) {
			t.Fatalf("untrusted prediction reached notifications: %+v", c)
		}
	}
}

func TestCommunityReceiptIsStillObservable(t *testing.T) {
	item := domain.Item{Text: "Got my reset.", Source: domain.SourceInfo{Kind: domain.Community, ResetContext: true}, Observations: []domain.AccountObservation{{Plan: "unknown", Status: "received"}}}
	c, err := (Rules{}).Classify(context.Background(), item)
	if err != nil || c.Type != domain.ResetPropagating || !c.Notify(true) {
		t.Fatalf("actual receipt lost: %+v %v", c, err)
	}
}

func TestLongEvidenceContainsActualResetClaim(t *testing.T) {
	text := strings.Repeat("Подробность продукта Codex, ", 70) + "usage limits will be restored tomorrow."
	c, err := (Rules{}).Classify(context.Background(), domain.Item{Text: text, Source: domain.SourceInfo{Kind: domain.FirstPartyDerived}})
	if err != nil || c.Type != domain.ResetAnnounced || !strings.Contains(c.Evidence, "usage limits will be restored tomorrow") || !strings.Contains(text, c.Evidence) || len([]rune(c.Evidence)) > 700 {
		t.Fatalf("actual reset missing from long evidence: %+v %v", c, err)
	}
}
