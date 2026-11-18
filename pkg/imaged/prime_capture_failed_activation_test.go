package imaged

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 005
// TestDeploymentReadyActivatesAfterFailedPrimeCapture pins deploy-path
// review P13 on the imaged side: a request deployment whose prime booted
// healthy and whose capture failed goes live on the STOPPED-instance proof,
// with no snapshot published, so its first request cold boots.
func TestDeploymentReadyActivatesAfterFailedPrimeCapture(t *testing.T) {
	store, handler, app, dep := seedModeActivation(t, api.ExecutionModeRequest)
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstanceWithMode(t.Context(), app.ID, dep.ID,
		string(state.StateColdBooting), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.RuntimeAppValuesForDeployment(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishOwnedInstanceRuntime(t.Context(), state.RuntimeInstancePublication{
		AccountID: app.AccountID, AppID: app.ID, InstanceID: instance.ID, NodeID: node.ID, WakeID: instance.WakeID,
		ExpectedState: "cold_booting", Netns: "capture-failed-prime", HostIP: "10.0.0.1", GuestUID: 20002, Fence: fence.SecretFence, ConfigFence: fence,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(t.Context(), instance.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	if err := handler.handleDeploymentReady(context.Background(), deploymentReadyPayload{
		DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeRequest,
		InstanceID: instance.ID, NoSnapshotReason: state.SnapshotPrimeCaptureFailedReason,
	}); err != nil {
		t.Fatalf("handleDeploymentReady(failed capture): %v", err)
	}
	updated, _ := store.DeploymentByID(context.Background(), dep.ID)
	if updated.Status != state.DeployLive {
		t.Fatalf("deployment status = %q; want live", updated.Status)
	}
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed-capture activation published a snapshot: %v", err)
	}
}
