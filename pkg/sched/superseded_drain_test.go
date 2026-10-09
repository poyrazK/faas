// adr: 198 — a superseded revision stops serving; its hot instances release
// their serving slots instead of waiting for the idle timeout.
// spec: §6 — schedd owns the instance state machine, one owner per app.

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// supersedeHotRevision wakes the app's first deployment on e, then marks a new
// deployment live, which supersedes the first one.
func supersedeHotRevision(t *testing.T, store *state.MemStore, e *Engine, app state.App) (oldDeploymentID, instanceID string) {
	t.Helper()
	ctx := context.Background()
	res, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake old revision: %v", err)
	}
	ins, err := store.InstanceByID(ctx, res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	newDep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, newDep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	old, err := store.DeploymentByID(ctx, ins.DeploymentID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if old.Status != state.DeploySuperseded {
		t.Fatalf("old deployment status = %q, want superseded", old.Status)
	}
	return ins.DeploymentID, res.InstanceID
}

func instanceState(t *testing.T, store *state.MemStore, id string) string {
	t.Helper()
	ins, err := store.InstanceByID(context.Background(), id)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	return ins.State
}

// TestDrainDeploymentInstances_OnlyAppOwnerDrains reproduces production-us
// hunt #8: every schedd received the superseded notification and three of
// them parked the same instance at once ("exclusive operation busy",
// "illegal edge stopped→parked"). A foreign schedd must leave it alone.
func TestDrainDeploymentInstances_OnlyAppOwnerDrains(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanFree, 128, 1)
	if err := store.SetAppNodeID(context.Background(), app.ID, "box-a"); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	ownerVMM := &fakeVMM{}
	owner := newEngine(t, store, ownerVMM, &fakeNotifier{}, "1.10.0").WithOwnerNodeID("box-a")
	oldDeploymentID, instanceID := supersedeHotRevision(t, store, owner, app)

	foreignVMM := &fakeVMM{}
	foreign := newEngine(t, store, foreignVMM, &fakeNotifier{}, "1.10.0").WithOwnerNodeID("box-b")
	foreign.drainDeploymentInstances(context.Background(), oldDeploymentID, true)
	if got := instanceState(t, store, instanceID); got != string(state.StateRunning) {
		t.Fatalf("foreign schedd drain: instance state = %q, want running", got)
	}
	if foreignVMM.snapshots != 0 || foreignVMM.destroys != 0 {
		t.Fatalf("foreign schedd touched the VM: snapshots=%d destroys=%d", foreignVMM.snapshots, foreignVMM.destroys)
	}

	owner.drainDeploymentInstances(context.Background(), oldDeploymentID, true)
	if got := instanceState(t, store, instanceID); got != string(state.StateParked) {
		t.Fatalf("owner drain: instance state = %q, want parked", got)
	}
	if ownerVMM.snapshots != 1 {
		t.Fatalf("owner snapshots = %d, want 1", ownerVMM.snapshots)
	}
}

// TestRunReaperDrainsSupersededRevisionAfterMissedNotification covers the
// durable repair: when the deployment_changed drain never ran (lost
// notification, failed park), the next reaper tick parks the superseded
// revision's hot instance even though it is not idle.
func TestRunReaperDrainsSupersededRevisionAfterMissedNotification(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanFree, 128, 1)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	_, instanceID := supersedeHotRevision(t, store, e, app)

	// The clock sits inside the Free idle timeout: idleness alone would
	// keep the instance resident.
	now := time.Now().Add(time.Second)
	loop := NewLoop(nil, e, testLog()).WithClock(func() time.Time { return now })
	loop.runReaper(context.Background())

	if got := instanceState(t, store, instanceID); got != string(state.StateParked) {
		t.Fatalf("instance state after reaper = %q, want parked", got)
	}
	if vmm.snapshots != 1 {
		t.Fatalf("snapshots = %d, want 1 (park preserves the rollback snapshot)", vmm.snapshots)
	}
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake live revision after repair: %v", err)
	}
}

// TestRunReaperKeepsLiveRevisionResident is the negative control: the repair
// acts only on superseded deployments, never on a live revision's instance.
func TestRunReaperKeepsLiveRevisionResident(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanFree, 128, 1)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	now := time.Now().Add(time.Second)
	loop := NewLoop(nil, e, testLog()).WithClock(func() time.Time { return now })
	loop.runReaper(context.Background())
	if got := instanceState(t, store, res.InstanceID); got != string(state.StateRunning) {
		t.Fatalf("live revision state after reaper = %q, want running", got)
	}
	if vmm.snapshots != 0 {
		t.Fatalf("snapshots = %d, want 0", vmm.snapshots)
	}
}
