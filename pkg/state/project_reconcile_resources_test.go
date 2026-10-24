package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectResourcesStore is the slice of a store the Compose resources
// contract needs (ADR-20261023120000).
type projectResourcesStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateProject(context.Context, state.Project) (state.Project, error)
	ApplyProjectReconcile(context.Context, state.Project, []state.ProjectReconcileMutation, []state.ProjectReconcileCron, state.ProjectScanSource, api.Limits) (state.ProjectReconcileResult, error)
	AppByID(context.Context, string) (state.App, error)
	SoftDeleteAppCascade(context.Context, string) (state.App, error)
}

func TestMemStoreProjectReconcileResources(t *testing.T) {
	testProjectReconcileResources(t, context.Background(), state.NewMemStore())
}

// testProjectReconcileResources pins SetResources: positive RAM, CPU share
// and max_concurrency replace the app's on update and restore, and an
// omitted flag or a zero value keeps them.
func testProjectReconcileResources(t *testing.T, ctx context.Context, store projectResourcesStore) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "project-resources@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "resources", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	draft := state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "resources-api", WorkloadName: "api",
		RAMMB: 512, CPUMillicores: 500, MaxConcurrency: 3}
	result, err := store.ApplyProjectReconcile(ctx, project, []state.ProjectReconcileMutation{{Op: "create", App: draft, SetResources: true}}, nil, state.ProjectScanSourceCompose, limits)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if app.RAMMB != 512 || app.CPUMillicores != 500 || app.MaxConcurrency != 3 {
		t.Fatalf("created ram=%d cpu=%d max=%d", app.RAMMB, app.CPUMillicores, app.MaxConcurrency)
	}
	for _, tc := range []struct {
		name          string
		restore, set  bool
		ram, cpu, max int
		want          [3]int
	}{
		{name: "update without the flag keeps them", ram: 1024, cpu: 1000, max: 5, want: [3]int{512, 500, 3}},
		{name: "update with the flag writes them", set: true, ram: 1024, cpu: 1000, max: 5, want: [3]int{1024, 1000, 5}},
		{name: "zero values are not written", set: true, ram: 256, want: [3]int{256, 1000, 5}},
		{name: "restore without the flag keeps them", restore: true, ram: 128, cpu: 250, max: 1, want: [3]int{256, 1000, 5}},
		{name: "restore with the flag writes them", restore: true, set: true, ram: 768, cpu: 250, want: [3]int{768, 250, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := state.ProjectReconcileMutation{Op: "update", App: app, SetResources: tc.set}
			mutation.App.RAMMB, mutation.App.CPUMillicores, mutation.App.MaxConcurrency = tc.ram, tc.cpu, tc.max
			if tc.restore {
				if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
					t.Fatal(err)
				}
				mutation.Op = "create"
				mutation.App.ID = ""
			}
			if _, err := store.ApplyProjectReconcile(ctx, project, []state.ProjectReconcileMutation{mutation}, nil, state.ProjectScanSourceCompose, limits); err != nil {
				t.Fatal(err)
			}
			got, err := store.AppByID(ctx, app.ID)
			if err != nil || [3]int{got.RAMMB, got.CPUMillicores, got.MaxConcurrency} != tc.want || got.Status != state.AppActive {
				t.Fatalf("ram=%d cpu=%d max=%d status=%s err=%v; want %v", got.RAMMB, got.CPUMillicores, got.MaxConcurrency, got.Status, err, tc.want)
			}
			app = got
		})
	}
}
