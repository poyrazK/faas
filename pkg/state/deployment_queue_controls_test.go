// deployment_queue_controls_test.go — ADR-124 state-machine pins
// for the new cancel/reorder/clear surface. Drives the memstore
// (every test suite that doesn't have a Postgres connection runs
// against it) and pins the CAS guards + the live-deployment block.
//
// The fixtures mirror pkg/builderd/builderd_test.go's
// seedDeploymentWithPlan so CreateApp/CreateDeployment/CreateBuild
// signatures stay in lockstep (those constructors take structs not
// positional args). Mirrors the cluster A pattern from spec §6.4
// amendment 1: bad-state transitions return the closed sentinel
// errors so the apid handler can route them to the canonical
// 409/402 Responses.
package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStore_CancelDeploymentTx_Live_Refuses(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	_, depID, _ := seedDeploymentWithPlan(t, store, "/tmp/src.tar.gz", string(api.PlanPro))
	// Force the deployment to live via the existing state
	// transitions so the test exercises the ErrCancelLiveForbidden
	// branch in CancelDeploymentTx.
	if err := store.UpdateDeploymentStatus(ctx, depID, DeployBuilding, ""); err != nil {
		t.Fatalf("seed building: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, depID, DeployImaging, ""); err != nil {
		t.Fatalf("seed imaging: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, depID, DeploySnapshotting, ""); err != nil {
		t.Fatalf("seed snapshotting: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, depID, DeployLive, ""); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	_, _, err := store.CancelDeploymentTx(ctx, depID, "user-test", CancelReasonUser)
	if !errors.Is(err, ErrCancelLiveForbidden) {
		t.Errorf("err = %v, want ErrCancelLiveForbidden", err)
	}
}

func TestStore_CancelDeploymentTx_Pending_Happy(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	buildID, depID, _ := seedDeploymentWithPlan(t, store, "/tmp/src.tar.gz", string(api.PlanPro))
	d, cascaded, err := store.CancelDeploymentTx(ctx, depID, "user-test", CancelReasonUser)
	if err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	if d.Status != DeployCancelled {
		t.Errorf("deployment status = %s, want %s", d.Status, DeployCancelled)
	}
	if d.CancelledAt == nil {
		t.Errorf("CancelledAt not stamped")
	}
	if d.CancelReason != string(CancelReasonUser) {
		t.Errorf("CancelReason = %q, want %q", d.CancelReason, CancelReasonUser)
	}
	if len(cascaded) != 1 || cascaded[0] != buildID {
		t.Errorf("cascaded build IDs = %v, want [%s]", cascaded, buildID)
	}
}

func TestStore_CancelDeploymentTx_Twice_Refuses(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	_, depID, _ := seedDeploymentWithPlan(t, store, "/tmp/src.tar.gz", string(api.PlanPro))
	if _, _, err := store.CancelDeploymentTx(ctx, depID, "user-test", CancelReasonUser); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	_, _, err := store.CancelDeploymentTx(ctx, depID, "user-test", CancelReasonUser)
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Errorf("err = %v, want ErrInvalidStateTransition", err)
	}
}

func TestStore_ReorderDeployment_OutOfRange_Refuses(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	_, depID, _ := seedDeploymentWithPlan(t, store, "/tmp/src.tar.gz", string(api.PlanPro))
	if err := store.ReorderDeployment(ctx, depID, 1001, "user-test"); !errors.Is(err, ErrPriorityOutOfRange) {
		t.Errorf("err = %v, want ErrPriorityOutOfRange", err)
	}
	if err := store.ReorderDeployment(ctx, depID, -1, "user-test"); !errors.Is(err, ErrPriorityOutOfRange) {
		t.Errorf("err = %v, want ErrPriorityOutOfRange (negative)", err)
	}
}

func TestStore_ReorderDeployment_NonPending_Refuses(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	_, depID, _ := seedDeploymentWithPlan(t, store, "/tmp/src.tar.gz", string(api.PlanPro))
	if err := store.UpdateDeploymentStatus(ctx, depID, DeployBuilding, ""); err != nil {
		t.Fatalf("seed building: %v", err)
	}
	if err := store.ReorderDeployment(ctx, depID, 100, "user-test"); !errors.Is(err, ErrReorderNotPending) {
		t.Errorf("err = %v, want ErrReorderNotPending", err)
	}
}

func TestStore_CancelReason_ClosedSet(t *testing.T) {
	for _, r := range []CancelReason{CancelReasonUser, CancelReasonAutoQuota, CancelReasonAutoHealth, CancelReasonSystem} {
		if !r.IsValid() {
			t.Errorf("reason %q expected valid", r)
		}
	}
	if CancelReason("nope").IsValid() {
		t.Errorf("reason %q expected invalid", "nope")
	}
}

// seedDeploymentWithPlan mirrors pkg/builderd/builderd_test.go's
// helper so the test can drive the canonical memstore surface
// (CreateAccount → CreateApp → CreateDeployment → CreateBuild) with
// the same struct-arg shape used by the live builderd suite. We
// avoid duplicating the helper because it already enforces the
// account-email uniqueness rule and the deployment-kind contract.
func seedDeploymentWithPlan(t *testing.T, store *MemStore, source, plan string) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "queue-ctrls@example.com", api.Plan(plan))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "queue-ctrls-app", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{
		AppID:       app.ID,
		Kind:        DeploymentKindTarball,
		SourcePath:  source,
		SourceBytes: 100,
		LogPath:     filepath.Join(t.TempDir(), "build.log"),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, DeploymentKindTarball, 100, dep.LogPath)
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return build.ID, dep.ID, app.ID
}
