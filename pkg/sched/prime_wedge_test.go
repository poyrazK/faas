package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSnapshotAndPark_PauseAndSnapshotCarriesDeadline pins the first half
// of the 2026-09-03 scheduler wedge.
//
// DialVMM's godoc states the contract: "per-call deadlines live at the
// engine call site". snapshotAndPark was the site that never got one, so
// PauseAndSnapshot inherited a context that could not expire. In
// production it blocked in grpc waitOnHeader for 10+ minutes.
//
// The assertion is on the deadline's presence rather than on an expiry,
// so the test stays fast — waiting out SnapshotTimeout would cost 25s.
func TestSnapshotAndPark_PauseAndSnapshotCarriesDeadline(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	vmm.mu.Lock()
	has, dl := vmm.snapHasDeadline, vmm.snapDeadline
	vmm.mu.Unlock()

	if !has {
		t.Fatal("PauseAndSnapshot got a context with NO deadline — a hung vmmd wedges the scheduler forever (see SnapshotTimeout)")
	}
	// Bound against the instance's own budget, not the flat
	// SnapshotTimeout: the budget scales with RAM (SnapshotBudgetFor),
	// so a fixed ceiling here would break every time the constants move.
	// The seeded app is 256 MB.
	want := SnapshotBudgetFor(256)
	if budget := time.Until(dl); budget <= 0 || budget > want+time.Second {
		t.Errorf("deadline budget = %s, want (0, %s]", budget, want+time.Second)
	}
}

// TestSnapshotTimeoutExceedsSweepBudget keeps the engine deadline above
// the watchdog's §6.1 SNAPSHOTTING budget. If the RPC deadline fired
// first, the watchdog would never see a stuck row in that state and the
// stuck-instance sweep for SNAPSHOTTING would be dead code. Mirrors how
// ColdBootTimeout (35s) sits above ColdBootSweepBudget (30s).
func TestSnapshotTimeoutExceedsSweepBudget(t *testing.T) {
	if SnapshotTimeout <= SnapshotSweepBudget {
		t.Errorf("SnapshotTimeout (%s) must exceed SnapshotSweepBudget (%s) so the watchdog trips first",
			SnapshotTimeout, SnapshotSweepBudget)
	}
}

// TestDispatchPrime_DoesNotBlockTheLoop pins the second half of the wedge.
//
// Loop.run selects over the pg_notify channel AND the reaper, cron and
// watchdog tickers on ONE goroutine. Prime used to run inline in that
// select, so a slow prime stalled the reaper and watchdog behind it and a
// hung prime stopped them forever — the SIGQUIT dump showed exactly this,
// with zero goroutines blocked on a mutex.
//
// A prime that takes longer than the loop can afford must not hold the
// caller. sleepFor is well above the time budget asserted below, so an
// inline Prime would blow it.
func TestDispatchPrime_DoesNotBlockTheLoop(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{sleepFor: 2 * time.Second}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	start := time.Now()
	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("handleNotification blocked %s on a slow prime; the reaper, cron and watchdog share this goroutine", elapsed)
	}
	loop.waitPrimes()
}

// TestDispatchPrime_RunsInlineWhenSaturated pins the deliberate fallback.
// Dropping a snapshot_prime strands the deployment in `snapshotting` with
// nothing to retry it — the notification is consumed and gone. Blocking
// the loop is the lesser harm, so saturation must degrade to inline
// execution, never to a drop.
func TestDispatchPrime_RunsInlineWhenSaturated(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	// Occupy every slot so dispatchPrime takes the default branch.
	loop.primeSlotsOnce.Do(func() { loop.primeSlots = make(chan struct{}, maxConcurrentPrimes) })
	for i := 0; i < maxConcurrentPrimes; i++ {
		loop.primeSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < maxConcurrentPrimes; i++ {
			<-loop.primeSlots
		}
	}()

	loop.dispatchPrime(context.Background(), app.ID, dep.ID)

	// Ran inline: the parked row exists without waiting on a worker.
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateParked) {
		t.Fatalf("rows = %+v, want one parked row (saturated dispatch must run inline, not drop)", rows)
	}
}
