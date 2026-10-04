package scheduler

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/httpio"
)

// Run invokes the task immediately and schedules the next run only after completion.
func Run(ctx context.Context, interval, jitter time.Duration, task func(context.Context) error, onError func(error)) {
	for ctx.Err() == nil {
		if err := task(ctx); err != nil && ctx.Err() == nil {
			onError(err)
		}
		delay := interval
		if jitter > 0 {
			delay += time.Duration(rand.Int64N(int64(jitter)))
		}
		if err := httpio.Pause(ctx, delay); err != nil {
			return
		}
	}
}
