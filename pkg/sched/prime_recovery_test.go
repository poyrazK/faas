package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func primeRecoveryFixture(t *testing.T) (*state.MemStore, state.App, state.Deployment, *Loop) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/layers/test.ext4", "layers/test.ext4", 1024); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}
	dep, err := store.PrepareDeploymentRollback(ctx, app.ID, dep.ID)
	if err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(3 * time.Minute) })
	return store, app, dep, loop
}

func TestPrimeRecovery_ReplaysLostRollbackHandoff(t *testing.T) {
	store, _, dep, loop := primeRecoveryFixture(t)
	loop.runPrimeRecovery(context.Background())
	loop.waitPrimes()
	instances, err := store.ListInstancesForApp(context.Background(), dep.AppID)
	if err != nil || len(instances) != 1 || instances[0].State != string(state.StateParked) {
		t.Fatalf("lost rollback prime was not recovered: instances=%+v err=%v", instances, err)
	}
	// imaged, not schedd, records the snapshot row after snapshot_written.
	// A second sweep must not cold-boot another VM while that handoff runs.
	loop.runPrimeRecovery(context.Background())
	loop.waitPrimes()
	instances, _ = store.ListInstancesForApp(context.Background(), dep.AppID)
	if len(instances) != 1 {
		t.Fatalf("parked prime duplicated before snapshot_written: %+v", instances)
	}
}

func TestPrimeRecovery_SkipsFreshStage(t *testing.T) {
	store, _, dep, loop := primeRecoveryFixture(t)
	loop.WithClock(time.Now)
	loop.runPrimeRecovery(context.Background())
	loop.waitPrimes()
	instances, _ := store.ListInstancesForApp(context.Background(), dep.AppID)
	if len(instances) != 0 {
		t.Fatalf("fresh snapshot_prepare should not be replayed: %+v", instances)
	}
}

func TestPrimeRecovery_IgnoresParkedInstanceFromPreviousPrime(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/layers/test.ext4", "layers/test.ext4", 1024); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	vmm := &fakeVMM{}
	notify := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notify, "1.10.0")
	if err := engine.Prime(ctx, app.ID, dep.ID); err != nil {
		t.Fatalf("initial Prime: %v", err)
	}
	if _, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: "snap/previous-prime", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}
	if _, err := store.PrepareDeploymentRollback(ctx, app.ID, dep.ID); err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(3 * time.Minute) })
	loop.runPrimeRecovery(ctx)
	loop.waitPrimes()
	instances, _ := store.ListInstancesForApp(ctx, app.ID)
	if len(instances) != 2 {
		t.Fatalf("older parked prime suppressed rollback recovery: %+v", instances)
	}
	if vmm.snapshots != 2 || notify.count("snapshot_written") != 2 {
		t.Fatalf("rollback reused old capture: captures=%d publications=%d, want 2/2", vmm.snapshots, notify.count("snapshot_written"))
	}
}

func TestPrimeRecovery_SkipsActivePrimeInstance(t *testing.T) {
	store, app, dep, loop := primeRecoveryFixture(t)
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateColdBooting), app.RAMMB, app.NodeID, "prime-in-flight"); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	loop.runPrimeRecovery(context.Background())
	loop.waitPrimes()
	instances, _ := store.ListInstancesForApp(context.Background(), dep.AppID)
	if len(instances) != 1 || instances[0].State != string(state.StateColdBooting) {
		t.Fatalf("active prime must not be duplicated: %+v", instances)
	}
}

func TestPrimeRecovery_SkipsExistingSnapshot(t *testing.T) {
	store, _, dep, loop := primeRecoveryFixture(t)
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: "snap/existing", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	loop.runPrimeRecovery(context.Background())
	loop.waitPrimes()
	got, err := store.LatestSnapshot(context.Background(), dep.ID)
	if err != nil || got.StorageKey != "snap/existing" {
		t.Fatalf("existing snapshot changed: (%+v, %v)", got, err)
	}
	instances, _ := store.ListInstancesForApp(context.Background(), dep.AppID)
	if len(instances) != 0 {
		t.Fatalf("existing snapshot must not be re-primed: %+v", instances)
	}
}

func TestPrimeRecovery_SkipsNonOwner(t *testing.T) {
	store, app, dep, loop := primeRecoveryFixture(t)
	if err := store.SetAppNodeID(context.Background(), app.ID, "owner-node"); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	loop.engine.ownerNodeID = "another-node"
	loop.runPrimeRecovery(context.Background())
	loop.waitPrimes()
	instances, _ := store.ListInstancesForApp(context.Background(), dep.AppID)
	if len(instances) != 0 {
		t.Fatalf("non-owner must not prime: %+v", instances)
	}
}
