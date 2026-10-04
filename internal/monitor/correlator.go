package monitor

import (
	"regexp"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

type Group struct {
	ID, Scope, Source, ExternalID, URL, Text, Hash string
	Rank                                           int
	LatestAt                                       time.Time
}

var postLink = regexp.MustCompile(`https://(?:www\.)?(?:x\.com|twitter\.com)/[A-Za-z0-9_]+/status/[0-9]+`)

func evidenceLink(text string) string {
	return strings.Replace(postLink.FindString(text), "twitter.com", "x.com", 1)
}

func similarity(a, b string) float64 {
	words := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, word := range strings.Fields(domain.Normalize(s)) {
			if len(word) > 2 {
				m[word] = true
			}
		}
		return m
	}
	first, second := words(a), words(b)
	intersection := 0
	for w := range first {
		if second[w] {
			intersection++
		}
	}
	union := len(first) + len(second) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// Correlate uses evidence before time proximity; separate banked and global resets never merge.
func Correlate(item domain.Item, c domain.Classification, groups []Group) *Group {
	var eligible []Group
	for _, g := range groups {
		if g.Scope != c.Scope {
			continue
		}
		if !item.PublishedAt.IsZero() && item.PublishedAt.Sub(g.LatestAt).Abs() > 36*time.Hour {
			continue
		}
		eligible = append(eligible, g)
		nearby := item.PublishedAt.Sub(g.LatestAt).Abs() <= 8*time.Hour
		if g.Source == item.Source.Name && g.ExternalID == item.ExternalID || nearby && g.Hash == domain.Hash(item.Text) {
			copy := g
			return &copy
		}
		a, b := evidenceLink(item.URL+" "+item.Text), evidenceLink(g.URL+" "+g.Text)
		if a != "" && a == b {
			copy := g
			return &copy
		}
		if nearby && similarity(item.Text, g.Text) >= .65 && g.Source != item.Source.Name {
			copy := g
			return &copy
		}
	}
	// A concise follow-up from the same author can advance one unambiguous open reset.
	if len(eligible) == 1 && c.Type.Rank() > eligible[0].Rank && eligible[0].Source == item.Source.Name {
		copy := eligible[0]
		return &copy
	}
	return nil
}
