// adr: 640

package sched

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// After a refused restore the owner marks the snapshot stale before it
// publishes RUNNING, so a deployment can have no non-stale snapshot while its
// WAKING row is still inside vmmd's cold-boot fallback. The row must still get
// the restore + cold-boot budget: production-us killed a 7.7 s fallback in
// exactly that window at the bare 5 s budget.
func TestWatchdogGivesEveryWakingRowTheFallbackBudget(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	waking, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(WAKING): %v", err)
	}
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); err == nil {
		t.Fatal("fixture has a non-stale snapshot; the test needs a deployment without one")
	}
	w := NewWatchdog(store, engine, slog.Default()).WithClock(time.Now)

	backdateInstance(t, store, waking.ID, 10*time.Second)
	w.sweepRuns(context.Background())
	if got := rowState(t, store, waking.ID); got != string(state.StateWaking) {
		t.Fatalf("WAKING row inside the fallback budget → %s, want WAKING", got)
	}
	vmm.mu.Lock()
	destroys := vmm.destroys
	vmm.mu.Unlock()
	if destroys != 0 {
		t.Fatalf("destroys = %d, want 0 inside the fallback budget", destroys)
	}

	backdateInstance(t, store, waking.ID, WakingSweepBudget+ColdBootSweepBudget+time.Second)
	w.sweepRuns(context.Background())
	if got := rowState(t, store, waking.ID); got == string(state.StateWaking) {
		t.Fatal("WAKING row past the fallback budget was not killed")
	}
}

// remotePublishStore publishes RUNNING right after KillStuck's read, the
// way the owning schedd on another node does: appMu is process-local.
type remotePublishStore struct {
	state.Store
	published bool
}

func (s *remotePublishStore) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	ins, err := s.Store.InstanceByID(ctx, id)
	if err == nil && !s.published && ins.State == string(state.StateWaking) {
		s.published = true
		if err := s.Store.UpdateInstanceStateIf(ctx, id, string(state.StateWaking), string(state.StateRunning)); err != nil {
			return state.Instance{}, err
		}
	}
	return ins, err
}

// KillStuck must claim a WAKING row before Destroy. Destroying first let a
// peer schedd publish RUNNING, after which WAKING→COLD_BOOTING was refused
// and the RUNNING row pointed at a VM that no longer existed (H5-14).
func TestKillStuckWakingYieldsToPeerPublication(t *testing.T) {
	base := state.NewMemStore()
	_, app, dep := seedApp(t, base, api.PlanPro, 512, 5)
	store := &remotePublishStore{Store: base}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	ins := admitAccountingInstance(t, base, engine, app, dep, state.StateWaking)

	if err := engine.KillStuck(context.Background(), ins.ID, app.ID, StuckWakingTimeout); err != nil {
		t.Fatalf("KillStuck: %v", err)
	}
	if !store.published {
		t.Fatal("peer publication was not simulated")
	}
	if got := rowState(t, base, ins.ID); got != string(state.StateRunning) {
		t.Fatalf("state = %s, want the peer's RUNNING", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys != 0 {
		t.Fatalf("destroys = %d, want 0: the peer's published guest was torn down", vmm.destroys)
	}
	if !engine.Ledger().ResidentFor(ins.ID) {
		t.Fatal("lost claim released the peer's reservation")
	}
}

// Winning the claim is what makes the owner's publication fail: its CAS
// expects WAKING, so it aborts and destroys instead of exposing a ghost.
func TestKillStuckWakingClaimFencesOwnerPublication(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	v := &heldAccountingDestroy{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), release: make(chan struct{})}
	engine := newEngine(t, store, v, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	ins := admitAccountingInstance(t, store, engine, app, dep, state.StateWaking)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- engine.KillStuck(ctx, ins.ID, app.ID, StuckWakingTimeout) }()
	select {
	case <-v.entered:
	case <-ctx.Done():
		t.Fatal("destroy did not start")
	}
	if err := store.UpdateInstanceStateIf(ctx, ins.ID, string(state.StateWaking), string(state.StateRunning)); err == nil {
		t.Fatal("owner published RUNNING after the watchdog claimed the row")
	}
	close(v.release)
	if err := <-result; err != nil {
		t.Fatalf("KillStuck: %v", err)
	}
	if got := rowState(t, store, ins.ID); got != string(state.StateColdBooting) {
		t.Fatalf("state = %s, want COLD_BOOTING", got)
	}
	if engine.Ledger().ResidentFor(ins.ID) {
		t.Fatal("confirmed teardown retained admission")
	}
}
