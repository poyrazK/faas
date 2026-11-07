package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectTombstoneStore is the slice of a store the project-reconcile
// restore contract needs.
type projectTombstoneStore interface {
	projectResourcesStore
	ScheduleAppDeletion(context.Context, string, time.Time) (state.App, error)
	ClaimAppDeletion(context.Context, string) error
	CreateCron(context.Context, string, string, string, bool) (state.Cron, error)
	CronByID(context.Context, string) (state.Cron, error)
}

func TestMemStoreProjectReconcileTombstoneGuards(t *testing.T) {
	testProjectReconcileTombstoneGuards(t, context.Background(), state.NewMemStore())
}

// testProjectReconcileTombstoneGuards pins that re-adding a deleted workload
// to a project restores its tombstone only as RestoreApp would: inside the
// grace window, with the crons the deletion suspended. Once the purge has
// claimed the tombstone (its artifacts are being deleted) the reconcile is
// refused instead of reviving an app with nothing left to boot.
func testProjectReconcileTombstoneGuards(t *testing.T, ctx context.Context, store projectTombstoneStore) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "project-tombstone@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "tombstone", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	readd := []state.ProjectReconcileMutation{{Op: "create", App: state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "tombstone-api", WorkloadName: "api"}}}
	result, err := store.ApplyProjectReconcile(ctx, project, readd, nil, state.ProjectScanSourceCompose, limits)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create: %+v, %v", result, err)
	}
	app := result.Added[0]
	cron, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyProjectReconcile(ctx, project, readd, nil, state.ProjectScanSourceCompose, limits); err != nil {
		t.Fatalf("re-add inside the grace window: %v", err)
	}
	if got, err := store.AppByID(ctx, app.ID); err != nil || got.Status != state.AppActive {
		t.Fatalf("restored app = %+v, %v; want active", got, err)
	}
	if got, err := store.CronByID(ctx, cron.ID); err != nil || got.SuspendedReason != "" {
		t.Fatalf("restored cron = %+v, %v; want unsuspended", got, err)
	}

	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimAppDeletion(ctx, app.ID); err != nil {
		t.Fatalf("ClaimAppDeletion: %v", err)
	}
	if _, err := store.ApplyProjectReconcile(ctx, project, readd, nil, state.ProjectScanSourceCompose, limits); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("re-add of a purging tombstone err = %v, want ErrConflict", err)
	}
	if got, err := store.AppByID(ctx, app.ID); err != nil || got.Status != state.AppDeleted {
		t.Fatalf("purging app = %+v, %v; want still deleted", got, err)
	}
}
