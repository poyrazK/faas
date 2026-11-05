// adr: 732
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedForkTarget(t *testing.T, store *state.MemStore, withSnapshot bool) state.AppFork {
	t.Helper()
	ctx := context.Background()
	acct, app, dep := seedApp(t, store, api.PlanPro, 256, 1)
	if withSnapshot {
		if _, err := store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 256 << 20,
			StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "c1"),
			Tier:       state.SnapshotTierInit,
		}); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
	}
	fork, err := store.CreateAppFork(ctx, state.CreateAppForkParams{
		AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, RequestedBy: "user:test",
		TTLSeconds: 600, MaxPerApp: 1, MaxPerAccount: 2, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateAppFork: %v", err)
	}
	return fork
}

// A restored fork is a RUNNING mode='fork' row on the pinned deployment,
// restored with Quarantine and no sealed secrets, reserved as KindFork so
// it does not consume the app's single serving slot.
func TestRestoreFork_QuarantinedNonServingRestore(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	fork := seedForkTarget(t, store, true)

	restored, err := e.RestoreFork(context.Background(), fork)
	if err != nil {
		t.Fatalf("RestoreFork: %v", err)
	}
	ins, err := store.InstanceByID(context.Background(), restored.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if ins.Mode != string(state.InstanceModeFork) || ins.State != string(state.StateRunning) || ins.DeploymentID != fork.DeploymentID {
		t.Fatalf("instance = %+v, want a running fork on the pinned deployment", ins)
	}
	vmm.mu.Lock()
	spec, restores := vmm.lastRestoreSpec, vmm.restores
	vmm.mu.Unlock()
	if restores != 1 || !spec.Quarantine || len(spec.SealedEnv) != 0 || len(spec.EgressAllowlist) != 0 || spec.StaticEgressIP != "" {
		t.Fatalf("restore spec = %+v (restores=%d), want one quarantined restore without secrets or egress inputs", spec, restores)
	}
	if restored.SnapshotID == "" {
		t.Error("restore did not report the snapshot it used")
	}

	// The fork holds RAM but not the app's one serving slot.
	if err := e.ledger.Admit(Request{
		Instance: "serving-1", AppID: fork.AppID, DeploymentID: fork.DeploymentID, Plan: api.PlanPro,
		RAMMB: 256, MaxConcurrency: 1, NodeID: ins.NodeID, NodeCeilingMB: 47600, VCPUBudget: 64,
	}); err != nil {
		t.Fatalf("a serving replica was refused while only a fork ran: %v", err)
	}
}

func TestRestoreFork_NoCaptureIsRefused(t *testing.T) {
	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	fork := seedForkTarget(t, store, false)
	if _, err := e.RestoreFork(context.Background(), fork); !errors.Is(err, ErrForkNoCapture) {
		t.Fatalf("err = %v, want ErrForkNoCapture", err)
	}
	if rows, _ := store.ListInstancesForApp(context.Background(), fork.AppID); len(rows) != 0 {
		t.Fatalf("a refused fork left %d instance rows", len(rows))
	}
}

func TestDestroyForkInstanceIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	fork := seedForkTarget(t, store, true)
	restored, err := e.RestoreFork(context.Background(), fork)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.DestroyForkInstance(context.Background(), fork.AppID, restored.InstanceID); err != nil {
			t.Fatalf("DestroyForkInstance: %v", err)
		}
	}
	vmm.mu.Lock()
	destroys, snapshots := vmm.destroys, vmm.snapshots
	vmm.mu.Unlock()
	if destroys != 1 || snapshots != 0 || e.ledger.ResidentFor(restored.InstanceID) {
		t.Fatalf("destroys=%d snapshots=%d resident=%v, want one destroy, no snapshot, released", destroys, snapshots, e.ledger.ResidentFor(restored.InstanceID))
	}
}
