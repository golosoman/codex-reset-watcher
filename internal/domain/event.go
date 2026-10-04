package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
)

type EventType string

const (
	GlobalResetConfirmed EventType = "global_reset_confirmed"
	BankedResetConfirmed EventType = "banked_reset_confirmed"
	ResetAnnounced       EventType = "reset_announced"
	ResetImminent        EventType = "reset_imminent"
	ResetSignal          EventType = "reset_signal"
	ResetCompleted       EventType = "reset_completed"
	NotRelevant          EventType = "not_relevant"
)

func (t EventType) Rank() int {
	switch t {
	case ResetSignal:
		return 1
	case ResetAnnounced:
		return 2
	case ResetImminent:
		return 3
	case GlobalResetConfirmed, BankedResetConfirmed:
		return 4
	case ResetCompleted:
		return 5
	default:
		return 0
	}
}

type SourceKind string

const (
	Official   SourceKind = "official"
	FirstParty SourceKind = "first_party"
	Community  SourceKind = "community"
)

type SourceInfo struct {
	Name         string     `json:"name"`
	Kind         SourceKind `json:"kind"`
	ResetContext bool       `json:"reset_context"`
}

type Item struct {
	Source      SourceInfo `json:"source"`
	ExternalID  string     `json:"external_id"`
	URL         string     `json:"url"`
	Text        string     `json:"text"`
	PublishedAt time.Time  `json:"published_at"`
}

type Classification struct {
	Type       EventType `json:"type"`
	Confidence float64   `json:"confidence"`
	Reason     string    `json:"reason"`
	Evidence   string    `json:"evidence"`
	Scope      string    `json:"scope"`
	Ambiguous  bool      `json:"ambiguous"`
}

type ClassifiedItem struct {
	Item           Item
	Classification Classification
}

type Event struct {
	ID          string     `json:"id"`
	GroupID     string     `json:"group_id"`
	Type        EventType  `json:"type"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Source      SourceInfo `json:"source"`
	SourceURL   string     `json:"source_url"`
	ExternalID  string     `json:"external_id"`
	PublishedAt time.Time  `json:"published_at"`
	DetectedAt  time.Time  `json:"detected_at"`
	Confidence  float64    `json:"confidence"`
	RawTextHash string     `json:"raw_text_hash"`
	Evidence    string     `json:"evidence"`
}

func Normalize(text string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text)), " ")
}

func Hash(text string) string {
	sum := sha256.Sum256([]byte(Normalize(text)))
	return hex.EncodeToString(sum[:])
}

func Limit(text string, count int) string {
	runes := []rune(text)
	if len(runes) <= count {
		return text
	}
	return string(runes[:count]) + "…"
}

func (c Classification) Notify(signals bool) bool {
	return c.Type.Rank() > 1 || c.Type == ResetSignal && signals
}

// Guard preserves source trust even if an optional model proposes a stronger label.
func Guard(item Item, c Classification) Classification {
	if c.Scope != "banked" {
		c.Scope = "global"
	}
	if c.Type.Rank() == 0 {
		c.Type = NotRelevant
	}
	if c.Type == BankedResetConfirmed {
		c.Scope = "banked"
	}
	if item.Source.Kind == Community && c.Type.Rank() >= 4 {
		c.Type = ResetSignal
		c.Reason = "Сообщество сообщает о reset; независимого подтверждения нет."
		c.Confidence = min(c.Confidence, 0.55)
	}
	return c
}
