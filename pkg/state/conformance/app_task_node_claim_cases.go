package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testAppTaskClaimRespectsNodeOwnership pins the node-scoped app task claim:
// a schedd only claims tasks for apps its node owns, because placement on any
// other node refuses the app and the task would fail before running.
func testAppTaskClaimRespectsNodeOwnership(t *testing.T, fx *Fixture) {
	claimer, ok := fx.Store.(state.NodeScopedAppTaskClaimer)
	if !ok {
		t.Fatal("store does not implement NodeScopedAppTaskClaimer")
	}
	peer := fx.Node
	peer.ID, peer.Name = "", "app-task-peer-"+uuid.NewString()
	peer, err := fx.Store.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetDeploymentRootfs(fx.Ctx, fx.Deployment.ID, "/local/app.ext4", "apps/conformance/node-claim.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	created, err := fx.Store.CreateAppTask(fx.Ctx, state.CreateAppTaskParams{
		AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"bin/check"}, CreatedAt: base,
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	claimedAt := base.Add(time.Second)
	if _, err := claimer.ClaimNextAppTaskForNode(fx.Ctx, "schedd-local", fx.Node.ID, claimedAt, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim by non-owner node = %v, want ErrNotFound", err)
	}
	claimed, err := claimer.ClaimNextAppTaskForNode(fx.Ctx, "schedd-peer", peer.ID, claimedAt, time.Minute)
	if err != nil || claimed.ID != created.ID || claimed.Status != state.AppTaskRestoring ||
		claimed.LeaseOwner == nil || *claimed.LeaseOwner != "schedd-peer" {
		t.Fatalf("claim by owner node = %+v, err=%v", claimed, err)
	}
}
