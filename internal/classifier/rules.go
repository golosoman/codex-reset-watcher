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
var negated = regexp.MustCompile(`(?i)\b(no|not|never|without)\b[^.!?\n]{0,35}\breset`)
var future = regexp.MustCompile(`(?i)\b(tomorrow|today|tonight|tuesday|wednesday|thursday|friday|saturday|sunday|monday|next week|will|coming|landing|lands|announc|by midnight)\b`)
var imminent = regexp.MustCompile(`(?i)\b(next hour|few minutes|shortly|imminent|about to|soon)\b`)
var completed = regexp.MustCompile(`(?i)\breset[s]? (all )?propagated\b|\ball reset for everyone\b`)
var confirmed = regexp.MustCompile(`(?i)have (been )?reset|has (been )?reset|\b(reset|resetting|reseting) (usage|limits|everyone|all)\b|\b(reset|resetting|reseting) (the )?(usage|rate|limits)|\bglobal reset (is here|complete|confirmed)|\bwe are (loading|adding)|\b(bank(ed)? reset|reset) (available|is available|added)\b`)
var pastConfirmed = regexp.MustCompile(`(?i)\b(have|has) (been )?reset\b`)
var futurePromise = regexp.MustCompile(`(?i)\bwill\b`)
var routine = regexp.MustCompile(`(?i)\breset[s]? (every|after|automatically|at the start)|\breset your (password|account|settings)|\bpassword reset\b`)
var signals = regexp.MustCompile(`(?i)burn (those|your|the) tokens|use (those|your|the) tokens (today|now)|spend (those|your|the|remaining) tokens`)

func (Rules) Classify(ctx context.Context, item domain.Item) (domain.Classification, error) {
	if err := ctx.Err(); err != nil {
		return domain.Classification{}, err
	}
	text := strings.TrimSpace(item.Text)
	c := domain.Classification{Type: domain.NotRelevant, Scope: "global"}
	if item.Source.Kind != domain.Official && item.Source.Kind != domain.FirstParty && item.Source.Kind != domain.Community {
		return c, nil
	}
	if strings.Contains(strings.ToLower(text), "banked") {
		c.Scope = "banked"
	}
	if routine.MatchString(text) || negated.MatchString(text) {
		return c, nil
	}
	contextual := product.MatchString(text) || item.Source.ResetContext
	if !contextual {
		return c, nil
	}
	alreadyReset := pastConfirmed.MatchString(text) && !futurePromise.MatchString(text)
	if signals.MatchString(text) {
		c.Type = domain.ResetSignal
		c.Confidence = .7
		c.Reason = "Ранний сигнал автора; reset ещё не подтверждён."
	} else if !reset.MatchString(text) {
		return c, nil
	} else if completed.MatchString(text) {
		c.Type = domain.ResetCompleted
		c.Confidence = .98
		c.Reason = "Автор прямо сообщает о завершении reset."
	} else if imminent.MatchString(text) && !alreadyReset {
		c.Type = domain.ResetImminent
		c.Confidence = .9
		c.Reason = "Reset ожидается в ближайшее время."
	} else if future.MatchString(text) && !alreadyReset {
		c.Type = domain.ResetAnnounced
		c.Confidence = .9
		c.Reason = "Объявлен будущий reset, но его завершение ещё не подтверждено."
	} else if confirmed.MatchString(text) {
		c.Type = domain.GlobalResetConfirmed
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
