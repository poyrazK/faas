package state

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCoordinatedPromotionMemory(t *testing.T) {
	testCoordinatedPromotion(t, func(*testing.T) Store { return NewMemStore() })
}

func testCoordinatedPromotion(t *testing.T, factory func(*testing.T) Store) {
	t.Run("workers", func(t *testing.T) { testPromotionWorkerMode(t, factory, true) })
	t.Run("migrations", func(t *testing.T) { testPromotionMigrationMode(t, factory, true) })
	t.Run("recovery", func(t *testing.T) { testPromotionRecoveryMode(t, factory, true) })
}

func newCoordinatedPromotionFixture(t *testing.T, store Store) *promotionSnapshotFixture {
	t.Helper()
	f := newPromotionSnapshotFixture(t, store, false, true, true, true, true)
	f.promotion.HealthPolicy = serviceGraphHealthPolicy(f)
	f.promotion.HealthPolicy.Worker = copyPromotionWorker(f.view.Worker)
	migration := *f.view.Migration
	f.promotion.HealthPolicy.Migration = &migration
	if len(f.apps) != 3 || len(f.promotion.HealthPolicy.Probes) != 2 {
		t.Fatal("coordinated fixture requires frontend, API and worker")
	}
	return f
}

func coordinatedBaselineMembers(f *promotionSnapshotFixture) []ProjectReleaseMember {
	var members []ProjectReleaseMember
	for _, d := range f.previous {
		members = append(members, ProjectReleaseMember{AppID: d.AppID, DeploymentID: d.ID})
	}
	return members
}

// Portable fixtures attest a task through the real store fences. These are
// state-machine tests, not proof of SQL execution or native guest lifecycle.
func completeCoordinatedMigration(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	ctx := t.Context()
	migrations := f.store.(ProjectPromotionMigrationStore)
	held, err := f.store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(ctx, f.apps[1].ID)
	requirePromotionSnapshot(t, err)
	if len(held) != 0 {
		t.Fatal("candidate worker started before schema expansion")
	}
	h, err := migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	id := h.Migration.TaskID
	h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if id == "" || h.Migration.TaskID != id || h.Phase != "migrating" {
		t.Fatal("migration admission was not idempotent")
	}
	tasks := f.store.(AppTaskStore)
	at := time.Now().UTC().Add(time.Millisecond)
	task, err := tasks.ClaimNextAppTask(ctx, "coordinated", at, time.Minute)
	requirePromotionSnapshot(t, err)
	snapshot, err := f.store.RuntimeAppValuesForDeployment(ctx, f.account.ID, task.AppID, task.DeploymentID)
	requirePromotionSnapshot(t, err)
	fence, err := NewRuntimeAppConfigFence(snapshot)
	requirePromotionSnapshot(t, err)
	requirePromotionSnapshot(t, f.store.(ProjectMigrationTaskRuntimeStore).BindProjectMigrationTaskRuntime(ctx, task, fence))
	node, err := f.store.ComputeNodeByName(ctx, DefaultLocalNodeName)
	requirePromotionSnapshot(t, err)
	_, err = f.store.(PromotionMigrationRuntimeStore).RegisterPromotionMigrationRuntime(ctx, task, node.ID)
	requirePromotionSnapshot(t, err)
	_, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(time.Millisecond))
	requirePromotionSnapshot(t, err)
	exit := 0
	_, err = tasks.CompleteAppTask(ctx, CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: AppTaskSucceeded, ExitCode: &exit, FinishedAt: at.Add(2 * time.Millisecond)})
	requirePromotionSnapshot(t, err)
	h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if h.Phase != "preflight" || h.Migration.TaskID != id || h.Migration.Status != "succeeded" || len(api.ProjectPromotionWorkers(&h.Policy)) == 0 {
		t.Fatal("migration lost the coordinated worker policy")
	}
}

func TestCoordinatedPromotionRejectsWorkerMigrationOwner(t *testing.T) {
	policy := &api.ProjectPromotionHealthPolicy{Migration: &api.ProjectPromotionMigrationPolicy{AppID: "worker", Slug: "worker", CommandHash: api.ProjectReleaseCommandHash([]string{"expand"}, false), Compatibility: "backward-compatible"}, Worker: &api.ProjectReleaseWorker{AppID: "worker", Slug: "worker", BindingID: "binding", BindingHash: strings.Repeat("a", 64), PreviousDeploymentID: "previous", DrainSeconds: 30, Status: "building"}, Probes: []api.ProjectReleaseProbe{{AppID: "api", Slug: "api", Method: "GET", Path: "/health"}}}
	if ValidatePromotionHealthPolicy(policy) == nil {
		t.Fatal("worker may not own the migration command")
	}
	policy.Migration.AppID = "api"
	if err := ValidatePromotionHealthPolicy(policy); err != nil {
		t.Fatal("valid independent migration and worker were refused", err)
	}
}

func assertCoordinatedWorkerDormant(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	held, err := f.store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(t.Context(), f.apps[1].ID)
	requirePromotionSnapshot(t, err)
	if len(held) != 0 {
		t.Fatal("migration failure or uncertainty held the candidate worker")
	}
}
