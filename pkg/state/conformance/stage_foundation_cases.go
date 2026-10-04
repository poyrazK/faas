// adr: 531
package conformance

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOwnedRuntimePublication(t *testing.T, fx *Fixture) {
	instance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateColdBooting), 256, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fx.Store.RuntimeAppValuesForDeployment(fx.Ctx, fx.Account.ID, fx.App.ID, fx.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	p := state.RuntimeInstancePublication{AccountID: fx.Account.ID, AppID: fx.App.ID, InstanceID: instance.ID, NodeID: fx.Node.ID, WakeID: instance.WakeID, ExpectedState: instance.State, Netns: "fc-owned", HostIP: "10.99.0.8", GuestUID: 20008, Fence: fence.SecretFence, ConfigFence: fence}
	forged := p
	forged.WakeID = uuid.NewString()
	if _, err := fx.Store.PublishOwnedInstanceRuntime(fx.Ctx, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign wake published: %v", err)
	}
	if _, err := fx.Store.InstanceRuntimeConfigFence(fx.Ctx, fx.Account.ID, fx.App.ID, instance.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected publication stored fence: %v", err)
	}
	published, err := fx.Store.PublishOwnedInstanceRuntime(fx.Ctx, p)
	if err != nil || published.State != string(state.StateRunning) || published.Netns != p.Netns {
		t.Fatalf("owned publication: %+v %v", published, err)
	}
	retained, err := fx.Store.InstanceRuntimeConfigFence(fx.Ctx, fx.Account.ID, fx.App.ID, instance.ID)
	if err != nil || !reflect.DeepEqual(retained, fence) {
		t.Fatalf("publication fence changed: %+v %v", retained, err)
	}
	if _, err := fx.Store.InstanceRuntimeConfigFence(fx.Ctx, uuid.NewString(), fx.App.ID, instance.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account read publication: %v", err)
	}
	if _, err := fx.Store.PublishOwnedInstanceRuntime(fx.Ctx, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale boot republished: %v", err)
	}
}

func testDeploymentScalingClocks(t *testing.T, fx *Fixture) {
	read := func() state.RuntimeScalingState {
		t.Helper()
		row, err := fx.Store.RuntimeScalingStateForDeployment(fx.Ctx, fx.Account.ID, fx.App.ID, fx.Deployment.ID)
		if err != nil || row.DeploymentID != fx.Deployment.ID {
			t.Fatalf("scaling owner: %+v %v", row, err)
		}
		return row
	}
	if row := read(); row.LastScaleInAt != nil || row.LastScaleOutAt != nil {
		t.Fatalf("new deployment inherited clocks: %+v", row)
	}
	if err := fx.Store.StampDeploymentScaleOut(fx.Ctx, fx.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	out := read()
	if out.LastScaleOutAt == nil || out.LastScaleInAt != nil {
		t.Fatalf("scale-out not persisted: %+v", out)
	}
	if err := fx.Store.StampDeploymentScaleIn(fx.Ctx, fx.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	both := read()
	if both.LastScaleInAt == nil || !both.LastScaleOutAt.Equal(*out.LastScaleOutAt) {
		t.Fatalf("scale-in replaced scale-out: %+v", both)
	}
	*both.LastScaleInAt = time.Time{}
	if read().LastScaleInAt.IsZero() {
		t.Fatal("caller changed persisted clock")
	}
	if _, err := fx.Store.RuntimeScalingStateForDeployment(fx.Ctx, uuid.NewString(), fx.App.ID, fx.Deployment.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account read scaling: %v", err)
	}
}

func testLayerDeletionClaims(t *testing.T, fx *Fixture) {
	key := "conformance/orphan/" + uuid.NewString()
	claim, eligible, err := fx.Store.ClaimLayerArtifactDeletion(fx.Ctx, key)
	if err != nil || !eligible || claim.DeletionID == "" || claim.State != state.LayerArtifactDeleting {
		t.Fatalf("deletion claim: %+v %t %v", claim, eligible, err)
	}
	replay, eligible, err := fx.Store.ClaimLayerArtifactDeletion(fx.Ctx, key)
	if err != nil || !eligible || replay.DeletionID != claim.DeletionID {
		t.Fatalf("deletion identity replaced: %+v %v", replay, err)
	}
	pending, err := fx.Store.PendingLayerArtifactDeletions(fx.Ctx)
	if err != nil || len(pending) != 1 || pending[0].DeletionID != claim.DeletionID {
		t.Fatalf("pending deletion lost: %+v %v", pending, err)
	}
	forged := claim
	forged.DeletionID = uuid.NewString()
	if err := fx.Store.CompleteLayerArtifactDeletion(fx.Ctx, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign deletion completed: %v", err)
	}
	for range 2 {
		if err := fx.Store.CompleteLayerArtifactDeletion(fx.Ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := fx.Store.PendingLayerArtifactDeletions(fx.Ctx); err != nil || len(pending) != 0 {
		t.Fatalf("completed deletion remains pending: %+v %v", pending, err)
	}
	retired, eligible, err := fx.Store.ClaimLayerArtifactDeletion(fx.Ctx, key)
	if err != nil || !eligible || retired.State != state.LayerArtifactDeleted || retired.DeletionID != claim.DeletionID {
		t.Fatalf("retired identity changed: %+v %v", retired, err)
	}
}

func testProductionQueueReader(t *testing.T, fx *Fixture) {
	for _, source := range []state.InvocationSource{state.InvocationQueue, state.InvocationAsyncInvoke} {
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: fx.App.ID, AccountID: fx.Account.ID, Source: source, Method: "POST", Path: "/work", Payload: []byte(`{"isolated":true}`), DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		row, err := fx.Store.ProductionQueueInvocationByID(fx.Ctx, inv.ID)
		if source == state.InvocationQueue {
			if err != nil || row.ID != inv.ID || string(row.Payload) != string(inv.Payload) {
				t.Fatalf("queue reader lost envelope: %+v %v", row, err)
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("non-queue source exposed: %v", err)
		}
	}
}
