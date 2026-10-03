package imaged

// adr: 137

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedModeActivation(t *testing.T, mode string) (*state.MemStore, *Handler, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "mode-activation-"+mode+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "mode-activation-" + mode,
		RAMMB:     256,
		Status:    state.AppActive,
		Manifest:  state.AppManifest{ExecutionMode: mode},
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:activation", Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	handler := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
	return store, handler, app, dep
}

func TestDeploymentReadyJobActivatesWithoutSnapshot(t *testing.T) {
	store, handler, _, dep := seedModeActivation(t, api.ExecutionModeJob)
	if err := handler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeJob,
	}); err != nil {
		t.Fatalf("handleDeploymentReady(job): %v", err)
	}
	updated, _ := store.DeploymentByID(context.Background(), dep.ID)
	if updated.Status != state.DeployLive {
		t.Fatalf("job deployment status = %q; want live", updated.Status)
	}
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("job activation published a snapshot: %v", err)
	}
}

func TestDeploymentReadyWorkerRequiresMatchingRunningInstance(t *testing.T) {
	store, handler, app, dep := seedModeActivation(t, api.ExecutionModeWorker)
	stopped, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateStopped), app.RAMMB, state.DefaultLocalNodeName, "wake-worker", string(state.InstanceModeWorker))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}
	err = handler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeWorker, InstanceID: stopped.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "running worker") {
		t.Fatalf("stopped worker readiness error = %v", err)
	}
	// adr: 521 — a stopped worker cannot borrow its former reservation.
	if err := store.UpdateInstanceState(context.Background(), stopped.ID, string(state.StateColdBooting)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("revived worker without fresh reservation: %v", err)
	}
	running, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateColdBooting), app.RAMMB, state.DefaultLocalNodeName, "wake-worker-fresh", string(state.InstanceModeWorker))
	if err != nil {
		t.Fatalf("reserve fresh worker: %v", err)
	}
	if err := store.UpdateInstanceState(context.Background(), running.ID, string(state.StateRunning)); err != nil {
		t.Fatalf("run worker state: %v", err)
	}
	if err := handler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeWorker, InstanceID: running.ID,
	}); err != nil {
		t.Fatalf("handleDeploymentReady(worker): %v", err)
	}
	updated, _ := store.DeploymentByID(context.Background(), dep.ID)
	if updated.Status != state.DeployLive {
		t.Fatalf("worker deployment status = %q; want live", updated.Status)
	}
}

func TestDeploymentReadyEphemeralSecretRequiresStoppedMatchingInstance(t *testing.T) {
	store, handler, app, dep := seedModeActivation(t, api.ExecutionModeRequest)
	instance, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateStopped), app.RAMMB, state.DefaultLocalNodeName, "prime-ephemeral", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}

	if err := handler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeRequest,
		InstanceID: instance.ID, NoSnapshotReason: "ephemeral_secret",
	}); err != nil {
		t.Fatalf("handleDeploymentReady(ephemeral request): %v", err)
	}
	updated, _ := store.DeploymentByID(context.Background(), dep.ID)
	if updated.Status != state.DeployLive {
		t.Fatalf("request deployment status = %q; want live", updated.Status)
	}
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ephemeral request activation published a snapshot: %v", err)
	}

	badStore, badHandler, badApp, badDep := seedModeActivation(t, api.ExecutionModeRequest)
	running, err := badStore.CreateInstanceWithMode(context.Background(), badApp.ID, badDep.ID,
		string(state.StateRunning), badApp.RAMMB, state.DefaultLocalNodeName, "unparked", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode(running): %v", err)
	}
	err = badHandler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: badDep.ID, ExecutionMode: api.ExecutionModeRequest,
		InstanceID: running.ID, NoSnapshotReason: "ephemeral_secret",
	})
	if err == nil || !strings.Contains(err.Error(), "stopped request instance") {
		t.Fatalf("running ephemeral readiness proof error = %v", err)
	}
	stillSnapshotting, _ := badStore.DeploymentByID(context.Background(), badDep.ID)
	if stillSnapshotting.Status != state.DeploySnapshotting {
		t.Fatalf("invalid ephemeral proof activated deployment: %q", stillSnapshotting.Status)
	}
}

func TestDeploymentReadyCannotBypassSnapshotModesOrTerminalState(t *testing.T) {
	store, handler, _, dep := seedModeActivation(t, api.ExecutionModeRequest)
	err := handler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeRequest,
	})
	if err == nil || !strings.Contains(err.Error(), "requires snapshot activation") {
		t.Fatalf("request readiness error = %v", err)
	}
	requestAfter, _ := store.DeploymentByID(context.Background(), dep.ID)
	if requestAfter.Status != state.DeploySnapshotting {
		t.Fatalf("request deployment bypassed snapshot gate: %q", requestAfter.Status)
	}

	jobStore, jobHandler, _, jobDep := seedModeActivation(t, api.ExecutionModeJob)
	if err := jobStore.UpdateDeploymentStatus(context.Background(), jobDep.ID, state.DeployCancelled, "cancelled"); err != nil {
		t.Fatalf("cancel deployment: %v", err)
	}
	err = jobHandler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: jobDep.ID, ExecutionMode: api.ExecutionModeJob,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot activate") {
		t.Fatalf("terminal readiness error = %v", err)
	}
	updated, _ := jobStore.DeploymentByID(context.Background(), jobDep.ID)
	if updated.Status != state.DeployCancelled {
		t.Fatalf("terminal deployment was revived: %q", updated.Status)
	}
}
