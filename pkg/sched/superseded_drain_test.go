// adr: 198 — a superseded revision stops serving; its hot instances release
// their serving slots instead of waiting for the idle timeout.
// spec: §6 — schedd owns the instance state machine, one owner per app.

package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// supersedeHotRevision wakes the app's first deployment on e, then marks a new
// deployment live, which supersedes the first one.
func supersedeHotRevision(t *testing.T, store *state.MemStore, e *sched.Engine, app state.App) (oldDeploymentID, instanceID string) {
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
	_, app, _ := sched.SeedApp(t, store, api.PlanFree, 128, 1)
	if err := store.SetAppNodeID(context.Background(), app.ID, "box-a"); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	ownerVMM := &sched.FakeVMM{}
	owner := sched.XNewEngine(t, store, ownerVMM, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID("box-a")
	oldDeploymentID, instanceID := supersedeHotRevision(t, store, owner, app)

	foreignVMM := &sched.FakeVMM{}
	foreign := sched.XNewEngine(t, store, foreignVMM, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID("box-b")
	foreign.DrainDeploymentInstances(context.Background(), oldDeploymentID, true)
	if got := instanceState(t, store, instanceID); got != string(state.StateRunning) {
		t.Fatalf("foreign schedd drain: instance state = %q, want running", got)
	}
	if (*foreignVMM.Snapshots()) != 0 || (*foreignVMM.Destroys()) != 0 {
		t.Fatalf("foreign schedd touched the VM: snapshots=%d destroys=%d", (*foreignVMM.Snapshots()), (*foreignVMM.Destroys()))
	}

	owner.DrainDeploymentInstances(context.Background(), oldDeploymentID, true)
	if got := instanceState(t, store, instanceID); got != string(state.StateParked) {
		t.Fatalf("owner drain: instance state = %q, want parked", got)
	}
	if (*ownerVMM.Snapshots()) != 1 {
		t.Fatalf("owner snapshots = %d, want 1", (*ownerVMM.Snapshots()))
	}
}

// TestRunReaperDrainsSupersededRevisionAfterMissedNotification covers the
// durable repair: when the deployment_changed drain never ran (lost
// notification, failed park), the next reaper tick parks the superseded
// revision's hot instance even though it is not idle.
func TestRunReaperDrainsSupersededRevisionAfterMissedNotification(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanFree, 128, 1)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	_, instanceID := supersedeHotRevision(t, store, e, app)

	// The clock sits inside the Free idle timeout: idleness alone would
	// keep the instance resident.
	now := time.Now().Add(time.Second)
	loop := sched.NewLoop(nil, e, sched.TestLog()).WithClock(func() time.Time { return now })
	loop.RunReaper(context.Background())

	if got := instanceState(t, store, instanceID); got != string(state.StateParked) {
		t.Fatalf("instance state after reaper = %q, want parked", got)
	}
	if (*vmm.Snapshots()) != 1 {
		t.Fatalf("snapshots = %d, want 1 (park preserves the rollback snapshot)", (*vmm.Snapshots()))
	}
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake live revision after repair: %v", err)
	}
}

// TestRunReaperKeepsLiveRevisionResident is the negative control: the repair
// acts only on superseded deployments, never on a live revision's instance.
func TestRunReaperKeepsLiveRevisionResident(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanFree, 128, 1)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	now := time.Now().Add(time.Second)
	loop := sched.NewLoop(nil, e, sched.TestLog()).WithClock(func() time.Time { return now })
	loop.RunReaper(context.Background())
	if got := instanceState(t, store, res.InstanceID); got != string(state.StateRunning) {
		t.Fatalf("live revision state after reaper = %q, want running", got)
	}
	if (*vmm.Snapshots()) != 0 {
		t.Fatalf("snapshots = %d, want 0", (*vmm.Snapshots()))
	}
}

// TestRunReaperEvictsCancelledRollbackTarget reproduces production-us hunt #9:
// a rollback moved a superseded revision that still had a hot instance back to
// snapshotting, the stale-deployment reconciler cancelled it, and the instance
// stayed running for 36 minutes. The reaper could not resolve a runtime policy
// for the cancelled deployment, skipped the row every tick, and the instance
// held the app's only concurrency slot so the live revision answered 429.
func TestRunReaperEvictsCancelledRollbackTarget(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanFree, 128, 1)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	oldDeploymentID, instanceID := supersedeHotRevision(t, store, e, app)
	if _, err := store.PrepareDeploymentRollback(ctx, app.ID, oldDeploymentID); err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if _, _, err := store.CancelDeploymentTx(ctx, oldDeploymentID, "system:stale-deployment-reconciler:snapshotting:80848s", state.CancelReasonSystem); err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	if got := instanceState(t, store, instanceID); got != string(state.StateRunning) {
		t.Fatalf("precondition: cancelled revision instance = %q, want running", got)
	}

	// Inside the Free idle timeout: idleness alone keeps the instance.
	now := time.Now().Add(time.Second)
	loop := sched.NewLoop(nil, e, sched.TestLog()).WithClock(func() time.Time { return now })
	loop.RunReaper(ctx)

	if got := instanceState(t, store, instanceID); got == string(state.StateRunning) {
		t.Fatalf("cancelled revision instance still running after reaper")
	}
	if (*vmm.Snapshots()) != 0 {
		t.Fatalf("snapshots = %d, want 0: nothing wakes a cancelled deployment", (*vmm.Snapshots()))
	}
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake live revision after repair: %v", err)
	}
}

// TestRunReaperDrainsAbortedCanaryCandidate reproduces production-us hunt #9:
// the circuit breaker aborted a canary (live, 0% traffic) whose instance stayed
// resident until the idle timeout, holding the rollout grant slot so the next
// deploy's hosting smoke answered 429 for four minutes.
func TestRunReaperDrainsAbortedCanaryCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, stable := sched.SeedApp(t, store, api.PlanFree, 128, 1)
	canary, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:canary",
		Status: state.DeployPending, TrafficPercent: 1, CanaryTotalSteps: 4, Scope: stable.Scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	res, err := e.Wake(ctx, app.ID, canary.ID, "", "")
	if err != nil {
		t.Fatalf("Wake canary: %v", err)
	}
	if _, _, err := store.RecoverRolloutForDeployment(ctx, app.ID, canary.ID, stable.ID, "abort", "circuit breaker: 5xx error rate regression"); err != nil {
		t.Fatalf("abort canary: %v", err)
	}
	aborted, err := store.DeploymentByID(ctx, canary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sched.ReaperAbortedCanary(aborted) {
		t.Fatalf("aborted canary = status %q traffic %d rollout %q; fixture does not match prod", aborted.Status, aborted.TrafficPercent, aborted.RolloutState)
	}

	now := time.Now().Add(time.Second)
	loop := sched.NewLoop(nil, e, sched.TestLog()).WithClock(func() time.Time { return now })
	loop.RunReaper(ctx)

	if got := instanceState(t, store, res.InstanceID); got != string(state.StateParked) {
		t.Fatalf("aborted canary instance state = %q, want parked", got)
	}
	if (*vmm.Snapshots()) != 1 {
		t.Fatalf("snapshots = %d, want 1 (an operator may still recover the candidate)", (*vmm.Snapshots()))
	}
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake stable after drain: %v", err)
	}
}

func TestReaperAbortedCanary(t *testing.T) {
	completed := time.Now()
	cases := []struct {
		name string
		d    state.Deployment
		want bool
	}{
		{"aborted canary at 0%", state.Deployment{Status: state.DeployLive, CanaryTotalSteps: 4, RolloutState: "aborted"}, true},
		{"canary still rolling out", state.Deployment{Status: state.DeployLive, TrafficPercent: 1, CanaryTotalSteps: 4, RolloutState: "rolling_out"}, false},
		{"staged 0% revision without a canary", state.Deployment{Status: state.DeployLive, RolloutState: "aborted"}, false},
		{"completed release later rolled back", state.Deployment{Status: state.DeployLive, CanaryTotalSteps: 4, RolloutState: "aborted", RolloutCompletedAt: &completed}, false},
		{"aborted canary still carrying traffic", state.Deployment{Status: state.DeployLive, TrafficPercent: 5, CanaryTotalSteps: 4, RolloutState: "aborted"}, false},
	}
	for _, tc := range cases {
		if got := sched.ReaperAbortedCanary(tc.d); got != tc.want {
			t.Errorf("%s: reaperAbortedCanary = %v, want %v", tc.name, got, tc.want)
		}
	}
}
