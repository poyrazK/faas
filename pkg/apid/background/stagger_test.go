package background

import (
	"context"
	"testing"
	"time"
)

func TestStaggeredRunsWithinTheSpread(t *testing.T) {
	const spread = 50 * time.Millisecond
	start := time.Now()
	ran := make(chan time.Duration, 1)
	Staggered(context.Background(), spread, func() { ran <- time.Since(start) })
	select {
	case took := <-ran:
		if took > spread+time.Second {
			t.Fatalf("loop started after %s, want within %s", took, spread)
		}
	default:
		t.Fatal("loop did not run")
	}
}

func TestStaggeredSkipsTheLoopWhenStoppedFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	Staggered(ctx, time.Hour, func() { ran = true })
	if ran {
		t.Fatal("loop ran after its context ended")
	}
}

func TestStaggeredWithoutSpreadRunsAtOnce(t *testing.T) {
	ran := false
	Staggered(context.Background(), 0, func() { ran = true })
	if !ran {
		t.Fatal("loop did not run")
	}
}
