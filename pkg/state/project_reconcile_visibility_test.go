// adr: 20261022131852 — a new Compose service without `ports:` is internal.
package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreProjectReconcileCreatesDraftVisibility(t *testing.T) {
	testProjectReconcileCreatesDraftVisibility(t, context.Background(), state.NewMemStore())
}

// testProjectReconcileCreatesDraftVisibility pins that a project create keeps
// the draft's visibility: an unpublished Compose service lands internal, and
// an empty draft value lands public.
func testProjectReconcileCreatesDraftVisibility(t *testing.T, ctx context.Context, store projectResourcesStore) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "project-visibility@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "visibility", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	mutations := []state.ProjectReconcileMutation{
		{Op: "create", App: state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "visibility-web", WorkloadName: "web"}},
		{Op: "create", App: state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "visibility-ledger", WorkloadName: "ledger", Visibility: api.AppVisibilityInternal}},
	}
	result, err := store.ApplyProjectReconcile(ctx, project, mutations, nil, state.ProjectScanSourceCompose, api.MustLimitsFor(api.PlanPro))
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	want := map[string]api.AppVisibility{"web": api.AppVisibilityPublic, "ledger": api.AppVisibilityInternal}
	for _, added := range result.Added {
		stored, err := store.AppByID(ctx, added.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := api.NormalizeAppVisibility(stored.Visibility); got != want[stored.WorkloadName] {
			t.Fatalf("%s visibility = %q, want %q", stored.WorkloadName, got, want[stored.WorkloadName])
		}
	}
}
