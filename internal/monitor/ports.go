package monitor

import (
	"context"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

type Source interface {
	Info() domain.SourceInfo
	Fetch(context.Context, time.Time) ([]domain.Item, error)
}
type Classifier interface {
	Classify(context.Context, domain.Item) (domain.Classification, error)
}
type Notification struct {
	ID       int64
	Event    *domain.Event `json:"event,omitempty"`
	Message  string        `json:"message,omitempty"`
	ChatID   string        `json:"chat_id"`
	Attempts int
}
type Notifier interface {
	Send(context.Context, Notification) (int64, error)
}
type SourceState struct {
	Initialized bool
	LastSuccess time.Time
	Failures    int
	LastError   string
}
type RecordOptions struct {
	NotifySignals bool
	ChatID        string
	MaxAge        time.Duration
}
type Run struct {
	ID               string
	Started          time.Time
	Duration         time.Duration
	Failures, Events int
}
type Store interface {
	Ping(context.Context) error
	SourceState(context.Context, string) (SourceState, error)
	Seen(context.Context, domain.Item) (bool, error)
	CommitSource(context.Context, domain.SourceInfo, []domain.ClassifiedItem, time.Time, RecordOptions) ([]domain.Event, error)
	SourceFailure(context.Context, string, string, time.Time, int, string) error
	SaveRun(context.Context, Run) error
	Pending(context.Context, time.Time) (*Notification, error)
	Claim(context.Context, int64, time.Time) (bool, error)
	Finish(context.Context, int64, string, int64, string, time.Time) error
}

// AmbiguousDelivery means the request may have been accepted without an observable response.
type AmbiguousDelivery struct{}

func (AmbiguousDelivery) Error() string { return "delivery outcome unknown" }

type DeliveryRejected struct {
	Code       int
	RetryAfter time.Duration
}

func (e *DeliveryRejected) Error() string { return "notification transport rejected request" }

type Observer interface {
	Source(context.Context, domain.SourceInfo, time.Duration, int, int, int, error)
	Cycle(context.Context, Run)
	Delivery(context.Context, string)
}
