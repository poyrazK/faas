package wire

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLivenessStallsAfterBudgetAndRecoversOnBeat(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000, 0)
	l := NewLiveness()
	l.now = func() time.Time { return now }
	l.Register("main", 10*time.Second)
	if !l.Healthy() {
		t.Fatal("fresh registration must be healthy")
	}
	now = now.Add(10 * time.Second)
	if !l.Healthy() {
		t.Fatal("age == budget is still healthy")
	}
	now = now.Add(time.Second)
	if got := l.Stalled(); len(got) != 1 || got[0] != "main" {
		t.Fatalf("stalled=%v want [main]", got)
	}
	l.Beat("main")
	if !l.Healthy() {
		t.Fatal("beat must clear the stall")
	}
	ages := l.Ages()
	if len(ages) != 1 || ages[0].Loop != "main" || ages[0].Age != 0 || ages[0].Budget != 10*time.Second {
		t.Fatalf("ages=%+v", ages)
	}
}

func TestLivenessUnknownLoopAndNilAreSafe(t *testing.T) {
	t.Parallel()
	var nilL *Liveness
	nilL.Register("x", time.Second)
	nilL.Beat("x")
	if !nilL.Healthy() || nilL.Stalled() != nil || nilL.Ages() != nil {
		t.Fatal("nil Liveness must report healthy and empty")
	}
	l := NewLiveness()
	l.Beat("never-registered") // no panic, no entry
	if len(l.Ages()) != 0 {
		t.Fatal("beat must not create loops")
	}
	l.Register("z", 0)
	if l.Ages()[0].Budget != time.Second {
		t.Fatal("non-positive budget clamps to 1s")
	}
}

func TestLivenessAgesSortedByName(t *testing.T) {
	t.Parallel()
	l := NewLiveness()
	for _, n := range []string{"sweep", "main", "runtime"} {
		l.Register(n, time.Minute)
	}
	ages := l.Ages()
	want := []string{"main", "runtime", "sweep"}
	for i, a := range ages {
		if a.Loop != want[i] {
			t.Fatalf("order=%v want %v", ages, want)
		}
	}
}

func TestStartWatchdogPublishesLoopGauges(t *testing.T) {
	ops := NewOpsMetrics("test")
	l := NewLiveness()
	// The sampler goroutine reads the clock concurrently with the
	// test advancing it, so the fake clock must be atomic.
	var nowNanos atomic.Int64
	nowNanos.Store(time.Now().UnixNano())
	l.now = func() time.Time { return time.Unix(0, nowNanos.Load()) }
	l.Register("main", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := StartWatchdog(ctx, l, ops, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer stop()
	// "runtime" is registered by StartWatchdog; move the clock past
	// main's budget and wait for a sample tick.
	nowNanos.Add(int64(6 * time.Second))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if testutil.ToFloat64(ops.loopStalled.WithLabelValues("main")) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if testutil.ToFloat64(ops.loopStalled.WithLabelValues("main")) != 1 {
		t.Fatal("main loop stalled gauge never reached 1")
	}
	if l.Healthy() {
		t.Fatal("watchdog predicate must be false while a loop is stalled")
	}
	if got := testutil.ToFloat64(ops.loopLastBeatAgeSeconds.WithLabelValues("main")); got < 6 {
		t.Fatalf("last beat age=%v want >= 6", got)
	}
}
