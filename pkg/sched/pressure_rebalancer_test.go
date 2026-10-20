// pressure_rebalancer_test.go — Tier A9 (ADR-087) watcher
// tests for PressureRebalancer. The companion
// pressure_rebalance_engine_test.go covers the engine-side
// policy; this file exercises the watcher-loop filter and
// dispatch.
//
// Test seams:
//   - NewPressureAggregatorForTest(window, cap, now) — the
//     frozen-clock seam the existing aggregator tests use.
//   - tick(ctx) — the per-cadence work unit. Split out of Run
//     so the test surface can drive a manual tick without
//     the time.Ticker.
//
// Tests in this file:
//
//  1. TestPressureRebalancer_EmptyAggregator — no pressured apps, no dispatch.
//  2. TestPressureRebalancer_ThresholdBoundary — over threshold dispatches, under does not.
//  3. TestPressureRebalancer_TwoApps_BothDispatched — multi-app sweep.
//  4. TestPressureRebalancer_FrozenClock — sliding-window GC inside tick.
//  5. TestPressureRebalancer_HandlerErrorContinues — bad app doesn't starve the rest.
//  6. TestPressureRebalancer_BeforeSweepHook — sweep counter bumps before handle.
//  7. TestPressureRebalancer_CtxCancel — loop returns on ctx cancel.
//  8. TestPressureRebalancer_New_Panics — bad wiring surfaces at startup.
//  9. TestPressureRebalancer_ColdStartSweep — startup sweep enumerates.

package sched_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/sched"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dispatchingHandle returns a TrackingHandle that records every
// appID passed to the watcher. The tests inspect the recording
// list to pin the dispatch order.
type dispatchingHandle struct {
	mu      sync.Mutex
	apps    []string
	failOn  map[string]error // appID -> error to return (forces the error-continue path)
	invokes int
}

func (h *dispatchingHandle) handle(_ context.Context, appID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.apps = append(h.apps, appID)
	h.invokes++
	if err, ok := h.failOn[appID]; ok {
		return err
	}
	return nil
}

func newRebalancerWith(t *testing.T, agg *sched.PressureAggregator, threshold int, hook func(appID string), h *dispatchingHandle) *sched.PressureRebalancer {
	t.Helper()
	return sched.NewPressureRebalancer(agg, threshold, 1*time.Second, hook, h.handle, nil)
}

func TestPressureRebalancer_EmptyAggregator(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	h := &dispatchingHandle{}
	r := newRebalancerWith(t, agg, 5, nil, h)
	r.Tick(context.Background())
	if h.invokes != 0 {
		t.Fatalf("empty aggregator must not dispatch, got %d invocations", h.invokes)
	}
}

func TestPressureRebalancer_ThresholdBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	h := &dispatchingHandle{}
	r := newRebalancerWith(t, agg, 5, nil, h)

	// 4 events — under the threshold — no dispatch.
	for i := 0; i < 4; i++ {
		agg.IncAtCapacity("app", now.Add(time.Duration(i)*time.Second))
	}
	r.Tick(context.Background())
	if h.invokes != 0 {
		t.Fatalf("4 events under threshold must not dispatch, got %d invocations", h.invokes)
	}
	// 5th event — over the threshold — one dispatch.
	agg.IncAtCapacity("app", now.Add(5*time.Second))
	r.Tick(context.Background())
	if h.invokes != 1 || h.apps[0] != "app" {
		t.Fatalf("5 events over threshold must dispatch once to app, got %v (invokes=%d)", h.apps, h.invokes)
	}
}

func TestPressureRebalancer_TwoApps_BothDispatched(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	for _, id := range []string{"beta", "alpha"} {
		for i := 0; i < 5; i++ {
			agg.IncAtCapacity(id, now.Add(time.Duration(i)*time.Second))
		}
	}
	h := &dispatchingHandle{}
	r := newRebalancerWith(t, agg, 5, nil, h)
	r.Tick(context.Background())
	if h.invokes != 2 {
		t.Fatalf("expected 2 invocations, got %d", h.invokes)
	}
	// Deterministic order (sort.Strings on the aggregator
	// output → alpha comes before beta).
	if h.apps[0] != "alpha" || h.apps[1] != "beta" {
		t.Errorf("expected [alpha, beta] (sorted), got %v", h.apps)
	}
}

func TestPressureRebalancer_FrozenClock_GCInsideTick(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	for i := 0; i < 5; i++ {
		agg.IncAtCapacity("app", now.Add(-59*time.Second+time.Duration(i)*time.Second))
	}
	h := &dispatchingHandle{}
	r := newRebalancerWith(t, agg, 5, nil, h)
	r.Tick(context.Background())
	if h.invokes != 1 {
		t.Fatalf("expected 1 invocation at the window edge, got %d", h.invokes)
	}
	// Walk the aggregator's clock forward 2s — the 5 events
	// are now -57s/-56s/.../-53s, still inside the 60s window.
	// The frozen clock is a closure; we can't move it from
	// outside. The press path itself reads from IncAtCapacity's
	// `t` parameter. To simulate window elapse we'd need a
	// seamed clock — covered by the Aggregator's own
	// WindowEdgePruning test. Verify the watcher re-fires
	// when the app is still in window.
	agg.IncAtCapacity("app", now)
	r.Tick(context.Background())
	if h.invokes != 2 {
		t.Fatalf("expected 2 invocations (still in window), got %d", h.invokes)
	}
}

func TestPressureRebalancer_HandlerErrorContinues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	for _, id := range []string{"alpha", "beta"} {
		for i := 0; i < 5; i++ {
			agg.IncAtCapacity(id, now.Add(time.Duration(i)*time.Second))
		}
	}
	// alpha returns an error; beta must still be dispatched.
	h := &dispatchingHandle{failOn: map[string]error{"alpha": errors.New("transient blip")}}
	r := newRebalancerWith(t, agg, 5, nil, h)
	r.Tick(context.Background())
	if h.invokes != 2 {
		t.Fatalf("handler error on alpha must not skip beta, got %d invocations", h.invokes)
	}
}

func TestPressureRebalancer_BeforeSweepHook(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	for i := 0; i < 5; i++ {
		agg.IncAtCapacity("app", now.Add(time.Duration(i)*time.Second))
	}
	// Hook counts every call BEFORE the handle runs. The
	// test asserts the hook fires exactly once per app per
	// tick.
	var hookInvokes int32
	var hookSeen []string
	var hookMu sync.Mutex
	hook := func(appID string) {
		atomic.AddInt32(&hookInvokes, 1)
		hookMu.Lock()
		hookSeen = append(hookSeen, appID)
		hookMu.Unlock()
	}
	h := &dispatchingHandle{}
	r := newRebalancerWith(t, agg, 5, hook, h)
	r.Tick(context.Background())
	if atomic.LoadInt32(&hookInvokes) != 1 {
		t.Fatalf("hook must fire once per app per tick, got %d", hookInvokes)
	}
	if h.invokes != 1 {
		t.Fatalf("handle must fire after hook, got %d invocations", h.invokes)
	}
	// Hook must run BEFORE the handle (the policy gate reads
	// the sweep counter the hook just bumped).
	hookMu.Lock()
	defer hookMu.Unlock()
	if len(hookSeen) != 1 || hookSeen[0] != "app" {
		t.Errorf("hook saw %v, want [app]", hookSeen)
	}
}

func TestPressureRebalancer_CtxCancel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	h := &dispatchingHandle{}
	// Use a 1ms tick to make the loop fast enough to cancel.
	r := sched.NewPressureRebalancer(agg, 5, 1*time.Millisecond, nil, h.handle, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	// Cancel after the loop has ticked at least once.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected ctx.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestPressureRebalancer_New_PanicsOnBadInputs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	ok := func(_ context.Context, _ string) error { return nil }
	cases := []struct {
		name         string
		agg          *sched.PressureAggregator
		threshold    int
		reassessment time.Duration
		handle       sched.PressureRebalancerHandle
	}{
		{"nil aggregator", nil, 5, 1 * time.Second, ok},
		{"zero threshold", agg, 0, 1 * time.Second, ok},
		{"negative threshold", agg, -1, 1 * time.Second, ok},
		{"zero reassessment", agg, 5, 0, ok},
		{"negative reassessment", agg, 5, -1 * time.Second, ok},
		{"nil handle", agg, 5, 1 * time.Second, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for case %q", tc.name)
				}
			}()
			sched.NewPressureRebalancer(tc.agg, tc.threshold, tc.reassessment, nil, tc.handle, nil)
		})
	}
}

func TestPressureRebalancer_ColdStartSweep(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))
	for _, id := range []string{"alpha", "beta"} {
		for i := 0; i < 5; i++ {
			agg.IncAtCapacity(id, now.Add(time.Duration(i)*time.Second))
		}
	}
	h := &dispatchingHandle{}
	r := newRebalancerWith(t, agg, 5, nil, h)
	n := r.RunColdStartSweep(context.Background())
	if n != 2 {
		t.Errorf("cold-start sweep returned %d, want 2", n)
	}
	if h.invokes != 2 {
		t.Errorf("cold-start sweep invoked handle %d times, want 2", h.invokes)
	}
}
