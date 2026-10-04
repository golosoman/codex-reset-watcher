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
	ResetPropagating     EventType = "reset_propagating"
	ResetConfirmed       EventType = "reset_confirmed"
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
	case GlobalResetConfirmed, BankedResetConfirmed, ResetConfirmed:
		return 4
	case ResetPropagating:
		return 5
	case ResetCompleted:
		return 6
	default:
		return 0
	}
}

type SourceKind string

const (
	Official          SourceKind = "official"
	FirstParty        SourceKind = "first_party"
	Community         SourceKind = "community"
	FirstPartyDerived SourceKind = "first_party_derived"
	Aggregator        SourceKind = "aggregator"
	Unverified        SourceKind = "unverified"
)

type SourceInfo struct {
	Name         string     `json:"name"`
	Kind         SourceKind `json:"kind"`
	ResetContext bool       `json:"reset_context"`
}

type Item struct {
	Source             SourceInfo           `json:"source"`
	ExternalID         string               `json:"external_id"`
	URL                string               `json:"url"`
	Text               string               `json:"text"`
	PublishedAt        time.Time            `json:"published_at"`
	SourceFetchedAt    time.Time            `json:"source_fetched_at,omitempty"`
	CanonicalOriginURL string               `json:"canonical_origin_url,omitempty"`
	CanonicalOriginID  string               `json:"canonical_origin_id,omitempty"`
	CanonicalAuthor    string               `json:"canonical_author,omitempty"`
	ConversationID     string               `json:"conversation_id,omitempty"`
	ReplyToID          string               `json:"reply_to_id,omitempty"`
	IsReply            bool                 `json:"is_reply,omitempty"`
	Context            string               `json:"context,omitempty"`
	ExpectedWindow     string               `json:"expected_window,omitempty"`
	ResetKind          string               `json:"reset_kind,omitempty"`
	Observations       []AccountObservation `json:"observations,omitempty"`
}

type AccountObservation struct {
	Plan       string    `json:"plan"`
	Status     string    `json:"status"`
	Before     *int      `json:"before,omitempty"`
	After      *int      `json:"after,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
	SourceURL  string    `json:"source_url"`
}

func Trusted(kind SourceKind) bool {
	return kind == Official || kind == FirstParty || kind == FirstPartyDerived
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
	ID                 string               `json:"id"`
	GroupID            string               `json:"group_id"`
	Type               EventType            `json:"type"`
	Title              string               `json:"title"`
	Summary            string               `json:"summary"`
	Source             SourceInfo           `json:"source"`
	SourceURL          string               `json:"source_url"`
	ExternalID         string               `json:"external_id"`
	PublishedAt        time.Time            `json:"published_at"`
	DetectedAt         time.Time            `json:"detected_at"`
	Confidence         float64              `json:"confidence"`
	RawTextHash        string               `json:"raw_text_hash"`
	Evidence           string               `json:"evidence"`
	Scope              string               `json:"scope,omitempty"`
	CanonicalOriginURL string               `json:"canonical_origin_url,omitempty"`
	CanonicalOriginID  string               `json:"canonical_origin_id,omitempty"`
	CanonicalAuthor    string               `json:"canonical_author,omitempty"`
	SourceFetchedAt    time.Time            `json:"source_fetched_at,omitempty"`
	ExpectedWindow     string               `json:"expected_window,omitempty"`
	Observations       []AccountObservation `json:"observations,omitempty"`
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
	if c.Scope != "banked" && c.Scope != "unknown" {
		c.Scope = "global"
	}
	if c.Type.Rank() == 0 {
		c.Type = NotRelevant
	}
	if c.Type == BankedResetConfirmed {
		c.Scope = "banked"
	}
	if !Trusted(item.Source.Kind) && c.Type.Rank() >= 4 {
		c.Type = ResetSignal
		for _, observation := range item.Observations {
			if observation.Status == "received" || observation.Status == "banked_reset_seen" {
				c.Type = ResetPropagating
				if observation.Status == "banked_reset_seen" {
					c.Scope = "banked"
				}
			}
		}
		c.Reason = "Есть наблюдения пользователей; полного распространения reset источник не подтверждает."
		if c.Type == ResetPropagating {
			c.Confidence = min(c.Confidence, 0.65)
		} else {
			c.Confidence = min(c.Confidence, 0.55)
			c.Reason = "Недоверенный источник сообщает о reset; независимого подтверждения нет."
		}
	}
	return c
}
