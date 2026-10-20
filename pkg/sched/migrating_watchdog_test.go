// migrating_watchdog_test.go — table-driven tests for the
// Tier A6 (ADR-067) MigratingWatchdog ticker.
//
// The watchdog is the smallest unit in the A6 slice: one
// time.Ticker, one Engine.ReconcileExpiredMigrations call per
// tick, no payload filtering (the input set is a DB query,
// not a pg_notify stream). The tests below pin:
//
//   - Ticker fires → handle called once per tick.
//   - Multiple ticks at the configured cadence → handle
//     called the right number of times.
//   - handle returns err → watchdog logs + continues; the
//     next tick is honoured.
//   - ctx cancel pre-tick → Run returns within.
//
// The engine-side policy (per-row active/dead owner dispatch,
// conditional UPDATE, audit row, metric) is exercised
// separately by pkg/sched/engine_test.go (TestReconcileExpiredMigrations_*).

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

// recordingMigratingWatchdogHandle is the test stand-in for
// Engine.ReconcileExpiredMigrations. Records every reconciler
// result and supports the same errAfter injection as the
// other recorders in this package.
type recordingMigratingWatchdogHandle struct {
	mu         sync.Mutex
	reconciled []int
	calls      atomic.Int32
	errAfter   *atomic.Int32
	err        error
}

func (r *recordingMigratingWatchdogHandle) fn(ctx context.Context) (int, error) {
	r.calls.Add(1)
	r.mu.Lock()
	n := int32(0)
	if r.errAfter != nil {
		n = r.errAfter.Load()
	}
	r.mu.Unlock()
	if r.errAfter != nil && n > 0 {
		r.errAfter.Add(-1)
		return 0, r.err
	}
	r.mu.Lock()
	r.reconciled = append(r.reconciled, 5)
	r.mu.Unlock()
	return 5, nil
}

func TestMigratingWatchdog_TickDispatches(t *testing.T) {
	rec := &recordingMigratingWatchdogHandle{}
	w := sched.NewMigratingWatchdog(rec.fn, 10*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	if err := sched.WaitFor(func() bool { return rec.calls.Load() >= 1 }, 200*time.Millisecond); err != nil {
		t.Fatalf("handle not called within 200ms: %v", err)
	}
	cancel()
	<-done
	if rec.calls.Load() == 0 {
		t.Fatalf("handle never called")
	}
}

func TestMigratingWatchdog_RespectsCadence(t *testing.T) {
	rec := &recordingMigratingWatchdogHandle{}
	w := sched.NewMigratingWatchdog(rec.fn, 10*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	if err := sched.WaitFor(func() bool { return rec.calls.Load() >= 3 }, 300*time.Millisecond); err != nil {
		t.Fatalf("handle not called >= 3 times within 300ms: %v (calls=%d)", err, rec.calls.Load())
	}
	cancel()
	<-done
	if rec.calls.Load() < 3 {
		t.Fatalf("calls=%d want >= 3", rec.calls.Load())
	}
}

func TestMigratingWatchdog_HandlesCtxCancel(t *testing.T) {
	rec := &recordingMigratingWatchdogHandle{}
	w := sched.NewMigratingWatchdog(rec.fn, 10*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err=%v want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Run did not return within 200ms of ctx cancel")
	}
}

func TestMigratingWatchdog_HandleErrorDoesNotStopLoop(t *testing.T) {
	rec := &recordingMigratingWatchdogHandle{}
	failUntil := atomic.Int32{}
	failUntil.Store(2) // fail twice, then succeed
	rec.errAfter = &failUntil
	rec.err = errors.New("synthetic PG blip")
	w := sched.NewMigratingWatchdog(rec.fn, 10*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	// After 4 ticks: 2 failures + 2 successes.
	if err := sched.WaitFor(func() bool { return rec.calls.Load() >= 4 }, 400*time.Millisecond); err != nil {
		t.Fatalf("handle not called >= 4 times within 400ms: %v (calls=%d)", err, rec.calls.Load())
	}
	cancel()
	<-done
	if rec.calls.Load() < 4 {
		t.Fatalf("calls=%d want >= 4 (a transient err must not stop the loop)", rec.calls.Load())
	}
}

func TestMigratingWatchdog_NilHandlePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewMigratingWatchdog(nil, ...) should panic")
		}
	}()
	_ = sched.NewMigratingWatchdog(nil, 10*time.Millisecond, nil)
}

func TestMigratingWatchdog_ZeroIntervalPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewMigratingWatchdog(..., 0, ...) should panic")
		}
	}()
	rec := &recordingMigratingWatchdogHandle{}
	_ = sched.NewMigratingWatchdog(rec.fn, 0, nil)
}
