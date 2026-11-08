package conformance

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testProjectReconcileAdoptsFunctionApp pins ADR-20261108160000 §1: a
// function workload whose slug a same-account function app outside any
// project holds takes that app over instead of failing, without counting
// toward the app quota; a plain app holding the slug still conflicts.
func testProjectReconcileAdoptsFunctionApp(t *testing.T, fx *Fixture) {
	reconciler, ok := fx.Store.(state.ProjectReconcileStore)
	if !ok {
		t.Fatal("store does not implement ProjectReconcileStore")
	}
	suffix := uuid.NewString()[:8]
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{
		AccountID: fx.Account.ID, Slug: "fnproj-" + suffix, ScanSource: state.ProjectScanSourceCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "fn-digest-" + suffix, Type: state.AppTypeFunction, Runtime: "python312",
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanScale)
	create := state.ProjectReconcileMutation{Op: "create", App: state.App{
		Slug: cli.Slug, Type: state.AppTypeFunction, Runtime: "python312", RootDir: "jobs/digest",
		WorkloadName: "digest", WorkloadClass: state.WorkloadClassHTTP, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
	}}
	res, err := reconciler.ApplyProjectReconcile(fx.Ctx, project, []state.ProjectReconcileMutation{create}, nil, state.ProjectScanSourceFunctions, limits)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0].ID != cli.ID || res.Added[0].ProjectID != project.ID || res.Added[0].RootDir != "jobs/digest" || res.Added[0].WorkloadName != "digest" {
		t.Fatalf("added = %+v, want the CLI app %s adopted into the project", res.Added, cli.ID)
	}

	plain, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "fn-plain-" + suffix, Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	conflict := create
	conflict.App.Slug, conflict.App.WorkloadName = plain.Slug, "plain"
	if _, err := reconciler.ApplyProjectReconcile(fx.Ctx, project, []state.ProjectReconcileMutation{conflict}, nil, state.ProjectScanSourceFunctions, limits); err == nil {
		t.Fatal("a plain app holding the slug must conflict")
	}
}
