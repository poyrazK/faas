package sched

// adr: 137

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func configureExecutionMode(t *testing.T, store *state.MemStore, app state.App, dep state.Deployment, mode string, status state.DeploymentStatus) state.App {
	t.Helper()
	manifest := app.Manifest
	manifest.ExecutionMode = mode
	updated, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatalf("UpdateApp manifest: %v", err)
	}
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, status, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	return updated
}

func lastNotifyPayload(t *testing.T, notifier *fakeNotifier, channel string) string {
	t.Helper()
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	for i := len(notifier.events) - 1; i >= 0; i-- {
		if notifier.events[i].channel == channel {
			return notifier.events[i].payload
		}
	}
	t.Fatalf("notification %q not emitted", channel)
	return ""
}

func TestPrimeJobIsArtifactOnly(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 4)
	configureExecutionMode(t, store, app, dep, api.ExecutionModeJob, state.DeploySnapshotting)
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime(job): %v", err)
	}
	if vmm.coldBoots != 0 || vmm.restores != 0 || vmm.snapshots != 0 {
		t.Fatalf("job deploy executed VM work: cold=%d restore=%d snapshot=%d", vmm.coldBoots, vmm.restores, vmm.snapshots)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil || len(instances) != 0 {
		t.Fatalf("job deploy instances = %d, %v; want none", len(instances), err)
	}
	payload := lastNotifyPayload(t, notifier, db.NotifyDeploymentReady)
	if strings.Contains(payload, "instance_id") {
		t.Fatalf("job readiness payload carries an instance: %s", payload)
	}
	var ready struct {
		ExecutionMode string `json:"execution_mode"`
	}
	if err := json.Unmarshal([]byte(payload), &ready); err != nil || ready.ExecutionMode != api.ExecutionModeJob {
		t.Fatalf("job readiness payload = %q, %v", payload, err)
	}
}

func TestPrimeWorkerRemainsRunningWithoutSnapshot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 4)
	configureExecutionMode(t, store, app, dep, api.ExecutionModeWorker, state.DeploySnapshotting)
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime(worker): %v", err)
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 || vmm.snapshots != 0 {
		t.Fatalf("worker prime calls: cold=%d restore=%d snapshot=%d; want 1/0/0", vmm.coldBoots, vmm.restores, vmm.snapshots)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("worker prime instances = %d, %v; want one", len(instances), err)
	}
	if instances[0].State != string(state.StateRunning) || instances[0].Mode != string(state.InstanceModeWorker) {
		t.Fatalf("worker prime instance = %+v; want RUNNING worker", instances[0])
	}
	if notifier.count(db.NotifySnapshotWritten) != 0 || notifier.count(db.NotifyDeploymentReady) != 1 {
		t.Fatalf("worker notifications: snapshot=%d ready=%d", notifier.count(db.NotifySnapshotWritten), notifier.count(db.NotifyDeploymentReady))
	}
}

func TestPrimeEphemeralSecretDoesNotCaptureInitSnapshot(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedApp(t, store, api.PlanHobby, 256, 4)
	configureExecutionMode(t, store, app, dep, api.ExecutionModeRequest, state.DeploySnapshotting)
	if err := store.UpsertAppSecretWithClassInScope(context.Background(), acct.ID, app.ID, api.DefaultEnvScope,
		"SESSION_TOKEN", "kid", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
		t.Fatalf("UpsertAppSecretWithClassInScope: %v", err)
	}
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime(ephemeral request): %v", err)
	}
	if vmm.coldBoots != 1 || vmm.snapshots != 0 || vmm.warmSnapshots != 0 || vmm.destroys != 1 {
		t.Fatalf("ephemeral prime calls: cold=%d init=%d warm=%d destroy=%d; want 1/0/0/1",
			vmm.coldBoots, vmm.snapshots, vmm.warmSnapshots, vmm.destroys)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil || len(instances) != 1 || instances[0].State != string(state.StateStopped) {
		t.Fatalf("ephemeral prime instances = %+v, %v; want one stopped instance", instances, err)
	}
	if notifier.count(db.NotifySnapshotWritten) != 0 || notifier.count(db.NotifyDeploymentReady) != 1 {
		t.Fatalf("ephemeral prime notifications: snapshot=%d ready=%d; want 0/1",
			notifier.count(db.NotifySnapshotWritten), notifier.count(db.NotifyDeploymentReady))
	}
	var ready struct {
		NoSnapshotReason string `json:"no_snapshot_reason"`
		InstanceID       string `json:"instance_id"`
	}
	if err := json.Unmarshal([]byte(lastNotifyPayload(t, notifier, db.NotifyDeploymentReady)), &ready); err != nil {
		t.Fatalf("decode deployment_ready: %v", err)
	}
	if ready.NoSnapshotReason != "ephemeral_secret" || ready.InstanceID != instances[0].ID {
		t.Fatalf("ephemeral readiness = %+v; want stopped instance proof", ready)
	}
}

func TestWorkerAdmissionNeverRestoresRequestSnapshot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 4)
	configureExecutionMode(t, store, app, dep, api.ExecutionModeWorker, state.DeployLive)
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID,
		FCVersion:    "1.10.0",
		MemBytes:     256 << 20,
		StorageKey:   SnapshotMemKey(dep.ID),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := engine.AdmitInstanceForDeployment(context.Background(), app.ID, dep.ID, dep.Scope, TriggerWorkerSingleton); err != nil {
		t.Fatalf("AdmitInstanceForDeployment(worker): %v", err)
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Fatalf("worker admission cold=%d restore=%d; want 1/0", vmm.coldBoots, vmm.restores)
	}
}

func TestReconcileWorkerAppKeepsNewestSingleton(t *testing.T) {
	store := state.NewMemStore()
	_, app, oldDep := seedApp(t, store, api.PlanPro, 256, 4)
	configureExecutionMode(t, store, app, oldDep, api.ExecutionModeWorker, state.DeployLive)
	newDep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(new): %v", err)
	}
	oldWorker, _ := store.CreateInstanceWithMode(context.Background(), app.ID, oldDep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "wake-old", string(state.InstanceModeWorker))
	newWorker, _ := store.CreateInstanceWithMode(context.Background(), app.ID, newDep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "wake-new", string(state.InstanceModeWorker))
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	engine.ReconcileWorkerApp(context.Background(), app.ID)
	engine.WaitWorkerStops()
	oldAfter, _ := store.InstanceByID(context.Background(), oldWorker.ID)
	newAfter, _ := store.InstanceByID(context.Background(), newWorker.ID)
	if oldAfter.State != string(state.StateStopped) || newAfter.State != string(state.StateRunning) {
		t.Fatalf("worker convergence old=%s new=%s; want stopped/running", oldAfter.State, newAfter.State)
	}
}
