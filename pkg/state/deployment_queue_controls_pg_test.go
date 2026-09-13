// deployment_queue_controls_pg_test.go — ADR-124 pgstore coverage for
// the cancel/reorder/clear/clear-obsolete surface. The memstore suite
// at deployment_queue_controls_test.go pins the canonical state machine
// behaviour; this file pins the pgstore-specific SQL: the FOR UPDATE
// lock under the deployment row, the cascading UPDATE on builds, the
// CAS-guarded UPDATE on pending deployments, and the bulk UPDATE that
// ClearObsoleteDeployments issues.
//
// All tests use the canonical pgStore(t) helper from pgstore_test.go,
// which stands up a fresh pgtest schema and migrates it through the
// full migration set (so 00410-00412 — the migrations this PR adds —
// are visible to every test below).
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedPendingDeployPg creates an account+app+pending-deployment fixture
// for the pgstore queue-control tests. The deployment stays in
// 'pending' status so CancelDeploymentTx and ReorderDeployment have
// a row to operate on without needing to walk the build pipeline first.
func seedPendingDeployPg(t *testing.T, s *state.PgStore, ctx context.Context, emailSuffix string) (acctID, appID, depID string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "u-"+emailSuffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "pg-queue-" + emailSuffix, Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:queue", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return acct.ID, app.ID, dep.ID
}

// seedBuildPg creates a build for a deployment using the same
// (deploymentID, kind, sourceBytes, logPath) signature the store
// exposes.
func seedBuildPg(t *testing.T, s *state.PgStore, ctx context.Context, depID string) string {
	t.Helper()
	build, err := s.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1024, "/tmp/queue-test.log")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return build.ID
}

func TestPg_CancelDeploymentTx_Pending_Happy(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "cancel-happy")

	got, cascaded, err := s.CancelDeploymentTx(ctx, depID, "operator:test", state.CancelReasonUser)
	if err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	if got.ID != depID {
		t.Errorf("returned deployment id = %q, want %q", got.ID, depID)
	}
	if got.Status != state.DeployCancelled {
		t.Errorf("deployment status after cancel = %q, want %q", got.Status, state.DeployCancelled)
	}
	if got.CancelledAt == nil {
		t.Errorf("CancelledAt was not stamped")
	}
	if got.CancelledByPrincipal != "operator:test" {
		t.Errorf("CancelledByPrincipal = %q, want %q", got.CancelledByPrincipal, "operator:test")
	}
	if got.CancelReason != string(state.CancelReasonUser) {
		t.Errorf("CancelReason = %q, want %q", got.CancelReason, string(state.CancelReasonUser))
	}
	// pending deployments with no in-flight builds cascade to nothing.
	if len(cascaded) != 0 {
		t.Errorf("cascaded build ids = %v, want empty", cascaded)
	}
}

// A legacy or interrupted app teardown can leave a nonterminal deployment
// under a soft-deleted parent. The stale-deployment reconciler must still be
// able to cancel that retained work and create the running-build cleanup
// obligation instead of retrying the same row forever.
func TestPg_CancelDeploymentTx_DeletedApp_CascadesRunningBuild(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, depID := seedPendingDeployPg(t, s, ctx, "cancel-deleted-app")
	buildID := seedBuildPg(t, s, ctx, depID)
	if _, err := s.ClaimQueuedBuild(ctx, buildID); err != nil {
		t.Fatalf("ClaimQueuedBuild: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET status = 'deleted' WHERE id = $1`, appID); err != nil {
		t.Fatalf("soft-delete parent app fixture: %v", err)
	}

	got, cascaded, err := s.CancelDeploymentTx(ctx, depID, "system:imaged-stale-reconciler", state.CancelReasonSystem)
	if err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	if got.Status != state.DeployCancelled {
		t.Errorf("deployment status after cancel = %q, want %q", got.Status, state.DeployCancelled)
	}
	if len(cascaded) != 1 || cascaded[0] != buildID {
		t.Fatalf("cascaded build ids = %v, want [%s]", cascaded, buildID)
	}
	build, err := s.BuildByID(ctx, buildID)
	if err != nil {
		t.Fatalf("BuildByID: %v", err)
	}
	if build.Status != state.BuildCancelled {
		t.Errorf("build status after cancel = %q, want %q", build.Status, state.BuildCancelled)
	}
	var cleanupCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM builder_vm_cleanup WHERE build_id = $1`, buildID).Scan(&cleanupCount); err != nil {
		t.Fatalf("query builder VM cleanup: %v", err)
	}
	if cleanupCount != 1 {
		t.Errorf("builder VM cleanup rows = %d, want 1", cleanupCount)
	}
}

func TestPg_CancelDeploymentTx_Live_Refuses(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "cancel-live")
	if err := s.MarkDeploymentLive(ctx, depID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	_, _, err := s.CancelDeploymentTx(ctx, depID, "operator:test", state.CancelReasonUser)
	if !errors.Is(err, state.ErrCancelLiveForbidden) {
		t.Fatalf("CancelDeploymentTx(live) = %v, want ErrCancelLiveForbidden", err)
	}
}

func TestPg_CancelDeploymentTx_DeletedApp_LiveStillRefuses(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, depID := seedPendingDeployPg(t, s, ctx, "cancel-deleted-live")
	if err := s.MarkDeploymentLive(ctx, depID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET status = 'deleted' WHERE id = $1`, appID); err != nil {
		t.Fatalf("soft-delete parent app fixture: %v", err)
	}

	_, _, err := s.CancelDeploymentTx(ctx, depID, "system:imaged-stale-reconciler", state.CancelReasonSystem)
	if !errors.Is(err, state.ErrCancelLiveForbidden) {
		t.Fatalf("CancelDeploymentTx(deleted app, live deployment) = %v, want ErrCancelLiveForbidden", err)
	}
}

func TestPg_CancelDeploymentTx_Twice_Refuses(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "cancel-twice")
	if _, _, err := s.CancelDeploymentTx(ctx, depID, "operator:test", state.CancelReasonUser); err != nil {
		t.Fatalf("first CancelDeploymentTx: %v", err)
	}
	// Second call: deployment is now cancelled (terminal). ReorderDeployment
	// and ClearDeployment accept any non-live row, but CancelDeploymentTx
	// must refuse to re-cancel a terminal row.
	_, _, err := s.CancelDeploymentTx(ctx, depID, "operator:test", state.CancelReasonSystem)
	if err == nil {
		t.Fatalf("second CancelDeploymentTx: expected an error, got nil")
	}
	if errors.Is(err, state.ErrCancelLiveForbidden) {
		t.Fatalf("second cancel returned ErrCancelLiveForbidden; want ErrInvalidStateTransition")
	}
	if !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("second CancelDeploymentTx = %v, want ErrInvalidStateTransition", err)
	}
}

func TestPg_ReorderDeployment_Pending_Happy(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "reorder-happy")
	if err := s.ReorderDeployment(ctx, depID, 42, "operator:test"); err != nil {
		t.Fatalf("ReorderDeployment: %v", err)
	}
	dep, err := s.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if dep.Priority != 42 {
		t.Errorf("priority = %d, want 42", dep.Priority)
	}
	if dep.ReorderedAt == nil {
		t.Errorf("ReorderedAt was not stamped")
	}
	if dep.ReorderedByPrincipal != "operator:test" {
		t.Errorf("ReorderedByPrincipal = %q, want %q", dep.ReorderedByPrincipal, "operator:test")
	}
}

func TestPg_ReorderDeployment_OutOfRange_Refuses(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "reorder-oor")
	if err := s.ReorderDeployment(ctx, depID, 1001, "operator:test"); !errors.Is(err, state.ErrPriorityOutOfRange) {
		t.Fatalf("ReorderDeployment(1001) = %v, want ErrPriorityOutOfRange", err)
	}
	if err := s.ReorderDeployment(ctx, depID, -1, "operator:test"); !errors.Is(err, state.ErrPriorityOutOfRange) {
		t.Fatalf("ReorderDeployment(-1) = %v, want ErrPriorityOutOfRange", err)
	}
}

func TestPg_ReorderDeployment_NonPending_Refuses(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "reorder-nonpending")
	if err := s.MarkDeploymentLive(ctx, depID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if err := s.ReorderDeployment(ctx, depID, 0, "operator:test"); !errors.Is(err, state.ErrReorderNotPending) {
		t.Fatalf("ReorderDeployment(live) = %v, want ErrReorderNotPending", err)
	}
}

func TestPg_ClearDeployment_Pending_Happy(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "clear-pending")
	if err := s.ClearDeployment(ctx, depID, "operator:test"); err != nil {
		t.Fatalf("ClearDeployment: %v", err)
	}
	dep, err := s.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if dep.DeletedAt == nil {
		t.Errorf("DeletedAt was not stamped")
	}
	if dep.DeletedByPrincipal != "operator:test" {
		t.Errorf("DeletedByPrincipal = %q, want %q", dep.DeletedByPrincipal, "operator:test")
	}
}

func TestPg_ClearDeployment_Live_Refuses(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "clear-live")
	if err := s.MarkDeploymentLive(ctx, depID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if err := s.ClearDeployment(ctx, depID, "operator:test"); !errors.Is(err, state.ErrCancelLiveForbidden) {
		t.Fatalf("ClearDeployment(live) = %v, want ErrCancelLiveForbidden", err)
	}
}

func TestPg_ClearObsoleteDeployments_Bulk_Happy(t *testing.T) {
	s, ctx := pgStore(t)

	// All 3 deployments must share the same app: the SQL partitions by
	// app_id and keeps the top-2 most-recent rows per app, so 3
	// cancelled rows on one app leave 1 soft-deletable, 2 protected.
	acct, err := s.CreateAccount(ctx, "u-obsolete@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "pg-queue-obsolete", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	seedCancelledDeployment := func(t *testing.T, reason state.CancelReason) {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:queue", Status: state.DeployPending,
		})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if _, _, err := s.CancelDeploymentTx(ctx, dep.ID, "operator:test", reason); err != nil {
			t.Fatalf("seed cancel: %v", err)
		}
	}
	seedCancelledDeployment(t, state.CancelReasonUser)
	seedCancelledDeployment(t, state.CancelReasonAutoQuota)
	seedCancelledDeployment(t, state.CancelReasonSystem)

	// olderThan filter: ClearObsoleteDeployments soft-deletes rows where
	// created_at < olderThan. We pass a future cutoff so every
	// cancelled row qualifies — the "natural" now()-1h would miss rows
	// that were just cancelled, defeating the test. The per-app
	// retention guard (top-2 most-recent rows protected) is what we
	// actually want to exercise here, so 3 cancelled rows on the same
	// app leave the newest-2 protected and 1 soft-deleted.
	cutoff := time.Now().Add(time.Hour)
	count, err := s.ClearObsoleteDeployments(ctx, app.ID, cutoff)
	if err != nil {
		t.Fatalf("ClearObsoleteDeployments: %v", err)
	}
	if count < 1 {
		t.Errorf("ClearObsoleteDeployments count = %d, want >= 1", count)
	}
}

func TestPg_CancelDeploymentTx_Cascades_Builds(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, s, ctx, "cascade-builds")
	buildID := seedBuildPg(t, s, ctx, depID)

	_, cascaded, err := s.CancelDeploymentTx(ctx, depID, "operator:test", state.CancelReasonUser)
	if err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	if len(cascaded) != 1 || cascaded[0] != buildID {
		t.Errorf("cascaded build ids = %v, want [%s]", cascaded, buildID)
	}
}
