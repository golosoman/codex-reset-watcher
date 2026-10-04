package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestImmediateNonOverlappingAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var active, maxActive, calls atomic.Int64
	go func() {
		Run(ctx, time.Millisecond, 0, func(context.Context) error {
			n := active.Add(1)
			if n > maxActive.Load() {
				maxActive.Store(n)
			}
			calls.Add(1)
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
			if calls.Load() == 3 {
				cancel()
			}
			return nil
		}, func(err error) { t.Error(err) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	if calls.Load() != 3 || maxActive.Load() != 1 {
		t.Fatalf("calls=%d max active=%d", calls.Load(), maxActive.Load())
	}
}
