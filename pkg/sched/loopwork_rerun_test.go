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

// A sweep with a backlog reruns itself pass after pass (ADR-20261029012000).
// Each pass records its own duration, so a long drain still shows passes
// completing to the routing-stalled alert.
func TestWorkPoolRecordsEachRerunPass(t *testing.T) {
	ops := wire.NewOpsMetrics("test")
	p := sched.NewWorkPool(sched.QuietLog(), ops)
	var passes atomic.Int32
	var pass func()
	pass = func() {
		if passes.Add(1) < 3 {
			if got := p.Submit(sched.WorkEventFanout, "tick", pass); got != "coalesced" {
				t.Errorf("backlog rerun request = %q, want coalesced", got)
			}
		}
	}
	if got := p.Submit(sched.WorkEventFanout, "tick", pass); got != "queued" {
		t.Fatalf("first sweep = %q, want queued", got)
	}
	p.Drain()
	if passes.Load() != 3 {
		t.Fatalf("passes = %d, want 3", passes.Load())
	}
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var samples uint64
	for _, family := range families {
		if family.GetName() != "test_loop_work_duration_seconds" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == string(sched.WorkEventFanout) {
					samples += metric.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	if samples != 3 {
		t.Fatalf("duration samples = %d, want one per pass (3)", samples)
	}
}
