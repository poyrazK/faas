// Phase 5 repo decomposition (PR-E): SoftDeleteAppCascade. Per the
// user decision recorded in /Users/poyrazk/.claude/plans/inherited-soaring-yao.md
// the cascade is *status-only*: the apps row is flipped to
// status='deleted' and the freshly-deleted row is returned so the
// caller (pkg/reconcile) can emit a project.workload.removed audit
// row. Child rows (app_envs, crons, custom_domains, etc.) survive
// the delete so an app deleted then re-created under the same slug
// keeps its envs and secrets — the slug-reuse invariant pinned by
// memstore_test.go:309-312. The GDPR-style hard cascade still lives
// in DeleteAccount.
//
//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_SoftDeleteAppCascade_UpdatesStatus pins the canonical
// happy path: a live app + SoftDeleteAppCascade returns the
// status=AppDeleted row.
func TestPg_SoftDeleteAppCascade_UpdatesStatus(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)

	deleted, err := s.SoftDeleteAppCascade(ctx, appID)
	if err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	if deleted.Status != state.AppDeleted {
		t.Errorf("status after soft-delete: got %q, want %q", deleted.Status, state.AppDeleted)
	}
	if deleted.ID != appID {
		t.Errorf("deleted.ID: got %q, want %q", deleted.ID, appID)
	}

	// Re-read must show the deleted status — the column was
	// persisted, not just returned.
	got, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID post-soft-delete: %v", err)
	}
	if got.Status != state.AppDeleted {
		t.Errorf("status persisted: got %q, want %q", got.Status, state.AppDeleted)
	}
}

// TestPg_SoftDeleteAppCascade_NotFound pins the missing-id contract.
// A subsequent reconcile that races with a delete-then-recreate would
// otherwise surface as a generic 22023 SQLSTATE; the mapErr funnel
// must map the no-rows result to ErrNotFound.
func TestPg_SoftDeleteAppCascade_NotFound(t *testing.T) {
	s, ctx := pgStore(t)
	// apps.id is UUID; the cast must succeed so the no-rows check
	// fires (SQLSTATE 22P02 would short-circuit at parse time).
	const missingID = "00000000-0000-0000-0000-000000000000"
	if _, err := s.SoftDeleteAppCascade(ctx, missingID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing id: got %v, want ErrNotFound", err)
	}
}

// spec: app deletion cancels active pipelines and fences late workers.
func TestPg_SoftDeleteAppCascade_CancelsActivePipeline(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindTarball, ImageDigest: "sha256:cancel-on-delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployBuilding, ""); err != nil {
		t.Fatal(err)
	}
	build, err := s.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 128, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDeleteAppCascade(ctx, appID); err != nil {
		t.Fatal(err)
	}
	gotDep, _ := s.DeploymentByID(ctx, dep.ID)
	if gotDep.Status != state.DeployCancelled {
		t.Fatalf("deployment = %q, want cancelled", gotDep.Status)
	}
	gotBuild, _ := s.BuildByID(ctx, build.ID)
	if gotBuild.Status != state.BuildCancelled {
		t.Fatalf("build = %q, want cancelled", gotBuild.Status)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("late transition = %v, want ErrInvalidStateTransition", err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("late live = %v, want ErrInvalidStateTransition", err)
	}
}

// TestPg_DeleteApp_LegacyWrapperStillWorks pins the apid deleteApp
// handler's call site. After PR-E the handler still calls DeleteApp
// (the legacy name); the wrapper must keep returning nil on success
// and the underlying status flip must persist.
func TestPg_DeleteApp_LegacyWrapperStillWorks(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)

	if err := s.DeleteApp(ctx, appID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	got, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID post-DeleteApp: %v", err)
	}
	if got.Status != state.AppDeleted {
		t.Errorf("status via legacy DeleteApp: got %q, want %q", got.Status, state.AppDeleted)
	}
}
