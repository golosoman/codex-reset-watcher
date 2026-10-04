package source

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

var percentChange = regexp.MustCompile(`(?i)(\d{1,3})\s*%\s*(?:→|->|to)\s*(\d{1,3})\s*%`)
var receivedReset = regexp.MustCompile(`(?i)reset (hit mine|appeared|arrived)|(?:got|received|see) (?:the |a |my )?(?:banked )?reset|(?:plus|pro(?: 5x| 20x)?|business) got it`)
var waitingReset = regexp.MustCompile(`(?i)no reset yet|still waiting|(?:didn.t|haven.t|not) (?:get|got|received|yet)`)
var plans = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"pro 20x", regexp.MustCompile(`\bpro 20x\b`)}, {"pro 5x", regexp.MustCompile(`\bpro 5x\b`)},
	{"business", regexp.MustCompile(`\bbusiness\b`)}, {"plus", regexp.MustCompile(`\bplus\b`)}, {"pro", regexp.MustCompile(`\bpro\b`)},
}

func Observations(item domain.Item) []domain.AccountObservation {
	text := strings.ToLower(item.Text)
	plan := "unknown"
	for _, candidate := range plans {
		if candidate.pattern.MatchString(text) {
			plan = candidate.name
			break
		}
	}
	status := "unknown"
	if waitingReset.MatchString(text) {
		status = "not_received"
	} else if receivedReset.MatchString(text) || percentChange.MatchString(text) {
		status = "received"
	}
	if status == "received" && strings.Contains(text, "banked") {
		status = "banked_reset_seen"
	}
	if status == "unknown" {
		return nil
	}
	observation := domain.AccountObservation{Plan: plan, Status: status, ObservedAt: item.PublishedAt, SourceURL: item.URL}
	if m := percentChange.FindStringSubmatch(text); m != nil {
		before, _ := strconv.Atoi(m[1])
		after, _ := strconv.Atoi(m[2])
		if before > 100 || after > 100 || after <= before {
			return nil
		}
		observation.Before, observation.After = &before, &after
	}
	return []domain.AccountObservation{observation}
}
