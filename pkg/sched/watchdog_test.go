package sched

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// backdateInstance rewinds the row's "age" anchor by the supplied
// delta. MemStore stamps started_at on creation; for SNAPSHOTTING
// the watchdog reads parked_at instead, so SetParkedAtForTest is
// used directly. The watchdog's ListInstancesByStatesOlderThan uses
// a state-aware column (started_at for WAKING/COLD_BOOTING,
// parked_at for SNAPSHOTTING), so a backdate on either anchor
// works for the relevant state.
func backdateInstance(t *testing.T, store state.Store, id string, age time.Duration) {
	t.Helper()
	mems := toMemStore(t, store)
	now := time.Now().Add(-age)
	mems.BackdateForTest(id, now)
}

// toMemStore asserts + unwraps the concrete *state.MemStore so the
// test-only backdating helpers can be called. The watchdog tests are
// MemStore-only — PgStore stamping would need its own SQL escape
// hatch, out of scope.
func toMemStore(t *testing.T, store state.Store) *state.MemStore {
	t.Helper()
	mems, ok := store.(*state.MemStore)
	if !ok {
		t.Skipf("watchdog tests require *state.MemStore; got %T", store)
	}
	return mems
}

// TestWatchdogSweepKillsStuck (commit 3, spec §6.1) drives one sweep
// with three instances whose "age" anchors are past their budgets:
// WAKING + 10s → COLD_BOOTING, COLD_BOOTING + 35s → FAILED,
// SNAPSHOTTING + 25s → STOPPED. Asserts all three terminal states
// land, the ledger reservation is released, and vmmd saw one
// Destroy per stuck row.
func TestWatchdogSweepKillsStuck(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

	waking, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(WAKING): %v", err)
	}
	coldBoot, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateColdBooting), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(COLD_BOOTING): %v", err)
	}
	snap, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateSnapshotting), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(SNAPSHOTTING): %v", err)
	}

	// Backdate so all three exceed their budgets. Admit a reservation
	// for each — the watchdog's Release must zero the ledger.
	engine.Ledger().Admit(Request{
		Instance: waking.ID, AppID: app.ID, Plan: api.PlanPro,
		RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	engine.Ledger().Admit(Request{
		Instance: coldBoot.ID, AppID: app.ID, Plan: api.PlanPro,
		RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	engine.Ledger().Admit(Request{
		Instance: snap.ID, AppID: app.ID, Plan: api.PlanPro,
		RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	// Snapshotting doesn't count for concurrency; BeginSnapshot drops it
	// from the concurrency counter but keeps RAM. Mirrors snapshotAndPark.
	engine.Ledger().BeginSnapshot(snap.ID)

	backdateInstance(t, store, waking.ID, 10*time.Second)
	backdateInstance(t, store, coldBoot.ID, 40*time.Second)
	// For SNAPSHOTTING, age is anchored on ParkedAt. Set it directly.
	mems := toMemStore(t, store)
	mems.SetParkedAtForTest(snap.ID, time.Now().Add(-25*time.Second))

	w := NewWatchdog(store, engine, slog.Default()).WithClock(func() time.Time { return time.Now() })
	w.sweepRuns(context.Background())

	// Each row must be in its terminal state.
	if got := rowState(t, store, waking.ID); got != string(state.StateColdBooting) {
		t.Errorf("WAKING row → %s, want COLD_BOOTING", got)
	}
	if got := rowState(t, store, coldBoot.ID); got != string(state.StateFailed) {
		t.Errorf("COLD_BOOTING row → %s, want FAILED", got)
	}
	if got := rowState(t, store, snap.ID); got != string(state.StateStopped) {
		t.Errorf("SNAPSHOTTING row → %s, want STOPPED", got)
	}

	// All three reservations must be released.
	if got := engine.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (3 reservations released)", got)
	}

	// Each KillStuck must have destroyed the vmmd VM.
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys < 3 {
		t.Errorf("destroys = %d, want 3", vmm.destroys)
	}

	// Metric counter records the (from, to) labels per kill.
	if got := testutil.ToFloat64(ops.WatchdogKills(string(StuckWakingTimeout), string(state.StateColdBooting))); got != 1 {
		t.Errorf("watchdog_kills{waking_timeout,cold_booting} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(ops.WatchdogKills(string(StuckColdBootTimeout), string(state.StateFailed))); got != 1 {
		t.Errorf("watchdog_kills{cold_boot_timeout,failed} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(ops.WatchdogKills(string(StuckSnapshotTimeout), string(state.StateStopped))); got != 1 {
		t.Errorf("watchdog_kills{snapshot_timeout,stopped} = %v, want 1", got)
	}
}

// TestWatchdogSweepLeavesYoungRowsAlone pins the negative half:
// instances not yet past the budget stay where they are.
func TestWatchdogSweepLeavesYoungRowsAlone(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	young, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateColdBooting), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	w := NewWatchdog(store, engine, slog.Default()).WithClock(func() time.Time { return time.Now() })
	w.sweepRuns(context.Background())

	if got := rowState(t, store, young.ID); got != string(state.StateColdBooting) {
		t.Errorf("young row state = %s, want COLD_BOOTING (unchanged)", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys != 0 {
		t.Errorf("destroys = %d, want 0 (young row untouched)", vmm.destroys)
	}
}

// TestEngineKillStuckRaceWithCompletion simulates a Wake that
// completed during the watchdog's planning time: by the time
// KillStuck re-reads the row under appMu, the row is already
// RUNNING. KillStuck must be a no-op (state mismatch).
func TestEngineKillStuckRaceWithCompletion(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	ins, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Race in: jump the row to RUNNING before KillStuck runs.
	if err := store.UpdateInstanceState(context.Background(), ins.ID, string(state.StateRunning)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}

	if err := engine.KillStuck(context.Background(), ins.ID, app.ID, StuckWakingTimeout); err != nil {
		t.Fatalf("KillStuck returned %v, want nil (state mismatch is a no-op)", err)
	}
	if got := rowState(t, store, ins.ID); got != string(state.StateRunning) {
		t.Errorf("state = %s, want RUNNING (KillStuck must not touch non-stuck rows)", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys != 0 {
		t.Errorf("destroys = %d, want 0 (no-op KillStuck must not destroy)", vmm.destroys)
	}
}

// TestWatchdogSweepRejectsUnknownReason pins that KillStuck refuses
// an unknown reason instead of silently no-op'ing (defensive — the
// code below the watchdog would mask programmer errors otherwise).
func TestWatchdogSweepRejectsUnknownReason(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	err := engine.KillStuck(context.Background(), "any", app.ID, StuckReason("not-a-real-reason"))
	if err == nil {
		t.Fatal("expected error for unknown reason, got nil")
	}
}

// TestLoopRunDrivesWatchdog (replaces TestLoopRunStartsWatchdogTicker,
// which had no assertions and didn't actually prove the Loop↔Watchdog
// wiring). This test seeds a stuck WAKING row with a real ledger
// reservation, drives loop.runWatchdog directly, and asserts:
//   - the row transitioned to COLD_BOOTING (per §6.1 fallback),
//   - the ledger reservation was released,
//   - vmmd saw a Destroy.
//
// It covers the path Loop.Run takes on its 4th select tick without
// needing to spin up a full Loop (which would require pg_notify).
func TestLoopRunDrivesWatchdog(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

	waking, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	engine.Ledger().Admit(Request{
		Instance: waking.ID, AppID: app.ID, Plan: api.PlanPro,
		RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	backdateInstance(t, store, waking.ID, 10*time.Second) // past WakingSweepBudget (5s)

	w := NewWatchdog(store, engine, slog.Default()).WithClock(func() time.Time { return time.Now() })
	loop := NewLoop(nil, engine, slog.Default()).WithWatchdog(w)

	// Drive the same code path Loop.Run's watchdog ticker would.
	loop.runWatchdog(context.Background())

	if got := rowState(t, store, waking.ID); got != string(state.StateColdBooting) {
		t.Errorf("WAKING row → %s, want COLD_BOOTING (watchdog fallback)", got)
	}
	if got := engine.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released)", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
	if got := testutil.ToFloat64(ops.WatchdogKills(string(StuckWakingTimeout), string(state.StateColdBooting))); got != 1 {
		t.Errorf("watchdog_kills{waking_timeout,cold_booting} = %v, want 1", got)
	}
}

// rowState is a small helper to read the state field with a useful
// failure message.
func rowState(t *testing.T, store state.Store, id string) string {
	t.Helper()
	ins, err := store.InstanceByID(context.Background(), id)
	if err != nil {
		t.Fatalf("InstanceByID(%s): %v", id, err)
	}
	return ins.State
}
