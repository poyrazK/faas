package sched_test

// adr: 191

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// An event_published wake that lands while a fanout sweep runs used to be
// coalesced away, leaving the new event for the 5 s safety tick. Now it asks
// for exactly one more sweep, however many wakes arrive during the pass.
func TestWorkPoolRerunsEventFanoutAfterCoalescedWake(t *testing.T) {
	p := sched.NewWorkPool(sched.QuietLog(), wire.NewOpsMetrics("test"))
	entered, gate := make(chan struct{}), make(chan struct{})
	var passes atomic.Int32
	first := func() {
		passes.Add(1)
		close(entered)
		<-gate
	}
	if got := p.Submit(sched.WorkEventFanout, "tick", first); got != "queued" {
		t.Fatalf("first sweep = %q, want queued", got)
	}
	<-entered
	var reruns atomic.Int32
	for range 3 {
		if got := p.Submit(sched.WorkEventFanout, "tick", func() { passes.Add(1); reruns.Add(1) }); got != "coalesced" {
			t.Fatalf("wake during a sweep = %q, want coalesced", got)
		}
	}
	close(gate)
	p.Drain()
	if passes.Load() != 2 || reruns.Load() != 1 {
		t.Fatalf("passes=%d reruns=%d, want one sweep and one rerun", passes.Load(), reruns.Load())
	}
	if got := p.Submit(sched.WorkEventFanout, "tick", func() {}); got != "queued" {
		t.Fatalf("sweep after the rerun = %q, want queued", got)
	}
	p.Drain()
}

// Kinds that do not ask for reruns still drop a coalesced duplicate.
func TestWorkPoolDoesNotRerunOtherKinds(t *testing.T) {
	p := sched.NewWorkPool(sched.QuietLog(), wire.NewOpsMetrics("test"))
	entered, gate := make(chan struct{}), make(chan struct{})
	if got := p.Submit(sched.WorkAppReconcile, "app-1", func() { close(entered); <-gate }); got != "queued" {
		t.Fatalf("first = %q, want queued", got)
	}
	<-entered
	var ran atomic.Bool
	if got := p.Submit(sched.WorkAppReconcile, "app-1", func() { ran.Store(true) }); got != "coalesced" {
		t.Fatalf("duplicate = %q, want coalesced", got)
	}
	close(gate)
	p.Drain()
	if ran.Load() {
		t.Fatal("a coalesced reconcile ran")
	}
}

// A sweep ending must never strand a wake: every wake is answered by a pass
// that starts after it, whether it lands mid-sweep or as the sweep ends,
// where it used to find the key gone but the one slot still held.
func TestWorkPoolEventFanoutNeverStrandsAWake(t *testing.T) {
	p := sched.NewWorkPool(sched.QuietLog(), wire.NewOpsMetrics("test"))
	var mu sync.Mutex
	latestWake, latestSeen := 0, 0
	for i := 1; i <= 500; i++ {
		mu.Lock()
		latestWake = i
		mu.Unlock()
		switch got := p.Submit(sched.WorkEventFanout, "tick", func() {
			mu.Lock()
			latestSeen = latestWake
			mu.Unlock()
			time.Sleep(50 * time.Microsecond) // the sweep's work: wakes land meanwhile
		}); got {
		case "queued", "coalesced":
		default:
			t.Fatalf("wake %d = %q, want queued or coalesced", i, got)
		}
	}
	p.Drain()
	if latestSeen != latestWake {
		t.Fatalf("last pass saw wake %d of %d", latestSeen, latestWake)
	}
}
