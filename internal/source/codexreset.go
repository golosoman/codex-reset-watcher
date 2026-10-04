package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

var originPath = regexp.MustCompile(`^/([A-Za-z0-9_]+)/status/([0-9]+)$`)

// CanonicalOrigin validates the publisher as well as the post, never a substring of a URL.
func CanonicalOrigin(raw string) (author, id, canonical string) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", "", ""
	}
	if u.Hostname() != "x.com" && u.Hostname() != "twitter.com" && u.Hostname() != "www.x.com" && u.Hostname() != "www.twitter.com" {
		return "", "", ""
	}
	m := originPath.FindStringSubmatch(strings.TrimSuffix(u.Path, "/"))
	if m == nil {
		return "", "", ""
	}
	author = strings.ToLower(m[1])
	return author, m[2], "https://x.com/" + author + "/status/" + m[2]
}

type CodexReset struct {
	Client       *httpio.Client
	URL          string
	Timeline     bool
	MaxStaleness time.Duration
	Now          func() time.Time
}

func (s CodexReset) Info() domain.SourceInfo {
	if s.Timeline {
		return domain.SourceInfo{Name: "codex-reset-timeline", Kind: domain.Aggregator, ResetContext: true}
	}
	return domain.SourceInfo{Name: "codex-reset-feed", Kind: domain.Aggregator, ResetContext: true}
}

func (s CodexReset) Fetch(ctx context.Context, since time.Time) ([]domain.Item, error) {
	body, err := s.Client.Get(ctx, s.URL, nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now()
	}
	maxAge := s.MaxStaleness
	if maxAge == 0 {
		maxAge = 2 * time.Hour
	}
	items, err := ParseCodexReset(body, s.Timeline, s.Info(), now, maxAge)
	if err != nil {
		return nil, err
	}
	return after(items, since), nil
}

func after(items []domain.Item, since time.Time) []domain.Item {
	result := make([]domain.Item, 0, len(items))
	for _, item := range items {
		if item.PublishedAt.IsZero() || item.PublishedAt.After(since) {
			result = append(result, item)
		}
	}
	return result
}

func ParseCodexReset(body []byte, timeline bool, info domain.SourceInfo, now time.Time, maxAge time.Duration) ([]domain.Item, error) {
	var data struct {
		FetchedAt time.Time `json:"fetched_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Stale     bool      `json:"stale"`
		Profile   struct {
			Handle string `json:"handle"`
		} `json:"profile"`
		Tweets []json.RawMessage `json:"tweets"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, &monitor.SourceProblem{Health: "degraded", Reason: "invalid codex-reset JSON"}
	}
	stamp, entries := data.FetchedAt, data.Tweets
	if timeline {
		stamp, entries = data.UpdatedAt, data.Events
	}
	if entries == nil || stamp.IsZero() {
		return nil, &monitor.SourceProblem{Health: "degraded", Reason: "codex-reset schema missing items or freshness time"}
	}
	if data.Stale || now.Sub(stamp) > maxAge || stamp.After(now.Add(5*time.Minute)) {
		return nil, &monitor.SourceProblem{Health: "stale", Reason: "codex-reset freshness watchdog: stale response"}
	}
	if len(entries) > 1000 {
		return nil, errors.New("too many codex-reset entries")
	}
	var items []domain.Item
	for _, raw := range entries {
		var entry struct {
			ID             string          `json:"id"`
			URL            string          `json:"url"`
			Text           string          `json:"text"`
			Summary        string          `json:"summary"`
			At             time.Time       `json:"at"`
			AnnouncedAt    time.Time       `json:"announced_at"`
			IsReply        bool            `json:"is_reply"`
			ReplyTo        string          `json:"in_reply_to_tweet_id"`
			ConversationID string          `json:"conversation_id"`
			Author         string          `json:"author"`
			ResetKind      string          `json:"reset_kind"`
			Source         string          `json:"source"`
			Context        string          `json:"context"`
			Window         json.RawMessage `json:"official_window"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, &monitor.SourceProblem{Health: "degraded", Reason: fmt.Sprintf("parse codex-reset item: %v", err)}
		}
		if timeline {
			entry.At = entry.AnnouncedAt
			if entry.Text == "" {
				entry.Text = entry.Summary
			}
		}
		if entry.ID == "" || strings.TrimSpace(entry.Text) == "" || entry.At.IsZero() || len(entry.Text) > 50000 {
			return nil, &monitor.SourceProblem{Health: "degraded", Reason: "codex-reset item missing ID, text or original date"}
		}
		author, id, origin := CanonicalOrigin(entry.URL)
		item := domain.Item{Source: info, ExternalID: entry.ID, URL: entry.URL, Text: entry.Text, PublishedAt: entry.At, SourceFetchedAt: stamp, CanonicalOriginURL: origin, CanonicalOriginID: id, CanonicalAuthor: author, IsReply: entry.IsReply, ReplyToID: entry.ReplyTo, ConversationID: entry.ConversationID, Context: entry.Context}
		if item.URL == "" {
			item.URL = "https://codex-reset.com/"
		}
		if entry.ResetKind == "banked" || entry.ResetKind == "global" {
			item.ResetKind = entry.ResetKind
		}
		// Timeline summaries are interpretations, not verbatim first-party evidence.
		if !timeline && author == "thsottiaux" && id == entry.ID && strings.EqualFold(data.Profile.Handle, author) && (entry.Author == "" || strings.EqualFold(entry.Author, author)) {
			item.Source.Kind = domain.FirstPartyDerived
		} else if !timeline {
			item.Source.Kind = domain.Unverified
		}
		if len(entry.Window) > 0 && string(entry.Window) != "null" {
			var window struct {
				Label string `json:"label"`
			}
			if err := json.Unmarshal(entry.Window, &window); err == nil {
				item.ExpectedWindow = domain.Limit(window.Label, 500)
			}
		}
		if timeline && entry.Source == "observed" && (entry.ResetKind == "banked" || strings.Contains(strings.ToLower(entry.Text), "reset")) {
			status := "received"
			if entry.ResetKind == "banked" {
				status = "banked_reset_seen"
			}
			item.Observations = []domain.AccountObservation{{Plan: "unknown", Status: status, ObservedAt: entry.At, SourceURL: item.URL}}
		}
		items = append(items, item)
	}
	return items, nil
}
