package wire_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// The old generation's drain outlives the restart grace until the unit stops
// it; then streams end at once and the drain ends one grace later.
func TestOldGenerationDrainRunsUntilStopped(t *testing.T) {
	stopping, stop := context.WithCancel(context.Background())
	var ended atomic.Int32
	const grace = 100 * time.Millisecond
	ctx, cancel := wire.OldGenerationDrain(stopping, grace, func() { ended.Add(1) }, nil)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("drain ended before the unit stopped it")
	case <-time.After(3 * grace):
	}
	if ended.Load() != 0 {
		t.Fatal("streams ended before the unit stopped the old generation")
	}
	stoppedAt := time.Now()
	stop()
	select {
	case <-ctx.Done():
		if elapsed := time.Since(stoppedAt); elapsed < grace {
			t.Fatalf("drain ended %v after the stop, want one grace (%v)", elapsed, grace)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not end after the stop")
	}
	if got := ended.Load(); got != 1 {
		t.Fatalf("endStreams ran %d times, want 1", got)
	}
}
