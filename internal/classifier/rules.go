package classifier

import (
	"context"
	"regexp"
	"strings"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

type Rules struct{}

var product = regexp.MustCompile(`(?i)\b(codex|chatgpt|usage|quota|tokens?|banked)\b`)
var reset = regexp.MustCompile(`(?i)\b(resets?|resetting|reseting|reseted|replenish(?:ed)?)\b`)
var negated = regexp.MustCompile(`(?i)\b(no|not|never|without)\b[^.!?\n]{0,35}\breset|\b(didn.t|haven.t|hasn.t|can.t|cannot)\b[^.!?\n]{0,35}\breset`)
var future = regexp.MustCompile(`(?i)\b(tomorrow|today|tonight|tuesday|wednesday|thursday|friday|saturday|sunday|monday|next week|will|coming|landing|lands|announc|by midnight)\b|\breset (at|on)\b`)
var imminent = regexp.MustCompile(`(?i)\b(next hour|few minutes|shortly|imminent|about to|soon)\b`)
var completed = regexp.MustCompile(`(?i)\breset[s]? (all )?propagated\b|\ball reset for everyone\b`)
var confirmed = regexp.MustCompile(`(?i)have (been )?reset|has (been )?reset|\b(reset|resetting|reseting) (usage|limits|everyone|all)\b|\b(reset|resetting|reseting) (the )?(usage|rate|limits)|\bglobal reset (is here|complete|confirmed)|\bwe are (loading|adding)|\b(bank(ed)? reset|reset) (available|is available|added)\b`)
var pastConfirmed = regexp.MustCompile(`(?i)\b(have|has) (been )?reset\b`)
var futurePromise = regexp.MustCompile(`(?i)\bwill\b`)
var routine = regexp.MustCompile(`(?i)\breset[s]? (every|after|automatically|at the start)|\breset your (password|account|settings)|\b(password|connection|factory|database|cache|api key) reset\b|\b(weekly|daily|monthly|scheduled|5h) (usage |quota )?reset\b`)
var signals = regexp.MustCompile(`(?i)burn (those|your|the) tokens|use (those|your|the) tokens|spend (those|your|the|remaining) tokens|more resets coming|who says it won.t reset|you know what.s coming`)
var globalScope = regexp.MustCompile(`(?i)\b(global|everyone|all|every account|all paid)\b`)
var bankedLoading = regexp.MustCompile(`(?i)\bloading (a |the )?banked reset\b`)
var restoredUsage = regexp.MustCompile(`(?i)\busage limits (will be |have been |were |are )?restored\b|\brestor(ed|ing) (the )?usage limits\b`)
var availableBanked = regexp.MustCompile(`(?i)\bbanked resets? (are |is )?available\b`)

func (Rules) Classify(ctx context.Context, item domain.Item) (domain.Classification, error) {
	if err := ctx.Err(); err != nil {
		return domain.Classification{}, err
	}
	text := strings.TrimSpace(item.Text)
	c := domain.Classification{Type: domain.NotRelevant, Scope: "global"}
	if item.Source.Kind != domain.FirstParty && !globalScope.MatchString(text) {
		c.Scope = "unknown"
	}
	if strings.Contains(strings.ToLower(text), "banked") {
		c.Scope = "banked"
	}
	if item.ResetKind == "banked" || item.ResetKind == "global" {
		c.Scope = item.ResetKind
	}
	if routine.MatchString(text) {
		return c, nil
	}
	contextual := product.MatchString(text) || item.Source.ResetContext
	if !contextual {
		return c, nil
	}
	if !domain.Trusted(item.Source.Kind) {
		for _, observation := range item.Observations {
			if observation.Status == "received" || observation.Status == "banked_reset_seen" {
				c.Type, c.Confidence, c.Evidence = domain.ResetPropagating, .65, domain.Limit(text, 700)
				return domain.Guard(item, c), nil
			}
		}
	}
	if negated.MatchString(text) && !signals.MatchString(text) {
		return c, nil
	}
	alreadyReset := pastConfirmed.MatchString(text) && !futurePromise.MatchString(text)
	if signals.MatchString(text) && domain.Trusted(item.Source.Kind) {
		c.Type = domain.ResetSignal
		c.Confidence = .7
		c.Reason = "Ранний сигнал автора; reset ещё не подтверждён."
	} else if !reset.MatchString(text) && !restoredUsage.MatchString(text) {
		return c, nil
	} else if completed.MatchString(text) && !futurePromise.MatchString(text) {
		c.Type = domain.ResetCompleted
		c.Confidence = .98
		c.Reason = "Автор прямо сообщает о завершении reset."
	} else if bankedLoading.MatchString(text) {
		c.Type = domain.ResetAnnounced
		c.Confidence = .9
		c.Reason = "Сообщается о подготовке сохранённого ручного reset; доступность ещё не подтверждена."
	} else if imminent.MatchString(text) && !alreadyReset {
		c.Type = domain.ResetImminent
		c.Confidence = .9
		c.Reason = "Reset ожидается в ближайшее время."
	} else if future.MatchString(text) && !alreadyReset {
		c.Type = domain.ResetAnnounced
		c.Confidence = .9
		c.Reason = "Объявлен будущий reset, но его завершение ещё не подтверждено."
	} else if confirmed.MatchString(text) || restoredUsage.MatchString(text) || availableBanked.MatchString(text) || strings.Contains(strings.ToLower(text), "reset is live") || strings.Contains(strings.ToLower(text), "resetting all paid users") || strings.EqualFold(strings.TrimSpace(text), "all reset") {
		c.Type = domain.GlobalResetConfirmed
		if c.Scope == "unknown" {
			c.Type = domain.ResetConfirmed
		}
		if c.Scope == "banked" {
			c.Type = domain.BankedResetConfirmed
		}
		c.Confidence = .95
		c.Reason = "Есть явное сообщение доверенного источника о reset."
	} else {
		c.Ambiguous = true
		c.Confidence = .3
	}
	if c.Type != domain.NotRelevant {
		c.Evidence = domain.Limit(text, 700)
	}
	return domain.Guard(item, c), nil
}
