// adr: 732
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Invariants 1 and 2 (spec §6.2) for forks: a fork never takes a serving
// slot, but its RAM is real and counts toward the node ceiling.
func TestForkReservationCountsRAMNotConcurrency(t *testing.T) {
	ledger := NewNodeLedger()
	base := Request{
		AppID: "app-1", DeploymentID: "dep-1", Plan: api.PlanPro,
		RAMMB: 256, VCPU: 1, CPUMillicores: 250, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 2 * (256 + api.PerVMOverheadMB), VCPUBudget: 8, CPUBudgetMillicores: 8000,
	}
	admit := func(id string, kind Kind) error {
		r := base
		r.Instance, r.Kind = id, kind
		return ledger.Admit(r)
	}
	if err := admit("serving-1", KindWake); err != nil {
		t.Fatalf("admit serving: %v", err)
	}
	if err := admit("fork-1", KindFork); err != nil {
		t.Fatalf("a fork must not need a serving slot: %v", err)
	}
	if err := admit("fork-2", KindFork); err == nil {
		t.Fatal("a third 264 MB reservation fit a 528 MB node: fork RAM was not counted")
	}
	ledger.Release("fork-1")
	if err := admit("serving-2", KindWake); err == nil {
		t.Fatal("second serving replica bypassed max concurrency after a fork released")
	}
}

func TestEngineStopInstance_ForkIsDestroyedNeverSnapshotted(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeFork))

	if _, err := e.StopInstance(context.Background(), ins.ID, StopOptions{}); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	assertForkDestroyed(t, e, rec, store, ins.ID)
}

// snapshotAndPark is the choke point behind the reaper, ParkApp, pressure
// and drain paths. A fork reaching it must be destroyed, not captured.
func TestSnapshotAndPark_ForkIsDestroyedNeverSnapshotted(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeFork))

	if err := e.snapshotAndPark(context.Background(), ins); err != nil {
		t.Fatalf("snapshotAndPark: %v", err)
	}
	assertForkDestroyed(t, e, rec, store, ins.ID)
}

func assertForkDestroyed(t *testing.T, e *Engine, rec *recordingStopVMM, store state.Store, id string) {
	t.Helper()
	rec.mu.Lock()
	snapshots, warm, destroys, signals := rec.snapshots, rec.warmSnapshots, rec.destroys, rec.stopInstanceOnNodeN
	rec.mu.Unlock()
	if snapshots != 0 || warm != 0 {
		t.Errorf("fork was captured: snapshots=%d warm=%d", snapshots, warm)
	}
	if destroys != 1 || signals != 0 {
		t.Errorf("destroys=%d signals=%d, want one destroy and no signal/grace", destroys, signals)
	}
	if e.ledger.ResidentFor(id) {
		t.Error("fork reservation not released")
	}
	got, err := store.InstanceByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", got.State)
	}
}
