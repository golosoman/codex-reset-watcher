package source

import (
	"context"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

type Optional struct {
	Source  monitor.Source
	Enabled bool
}

func (s Optional) Active() bool { return s.Enabled }

func (s Optional) Info() domain.SourceInfo { return s.Source.Info() }
func (s Optional) Fetch(ctx context.Context, since time.Time) ([]domain.Item, error) {
	if !s.Enabled {
		return nil, monitor.ErrSourceDisabled
	}
	return s.Source.Fetch(ctx, since)
}

type Fallback struct {
	Optional
	Primary string
}

func (s Fallback) FallbackFor() string { return s.Primary }
