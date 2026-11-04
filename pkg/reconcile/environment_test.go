package reconcile_test

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemReconcileEnvironmentPreservesProduction(t *testing.T) {
	testReconcileEnvironmentPreservesProduction(t, state.NewMemStore())
}

func testReconcileEnvironmentPreservesProduction(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "environment-reconcile@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	makeApp := func(name string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name,
			Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP, RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 2,
			Manifest: state.AppManifest{ProjectImage: "docker.io/library/nginx:1.26", ProjectSourceSHA256: "production-source"}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	frontend, legacy := makeApp("frontend"), makeApp("legacy")
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := reconcile.NewService(store, audit.New(store, log, nil, "reconcile"), log)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "frontend", Image: "docker.io/library/nginx:1.27", Ports: []int{80}, Class: reposcan.ClassHTTP, SourceSHA256: "stage-source", Tier: reposcan.TierCompose,
			Resources: &api.ComposeResources{MemoryMB: 512}},
		{Name: "backend", Image: "docker.io/library/nginx:1.27", Ports: []int{80}, Class: reposcan.ClassHTTP, SourceSHA256: "stage-backend", Tier: reposcan.TierCompose},
	}}
	result, err := service.ReconcileEnvironment(ctx, project, scan, "staging", "source", false)
	if err != nil || len(result.Added) != 1 || len(result.Changed) != 1 || len(result.Removed) != 0 {
		t.Fatalf("stage result: added=%d changed=%d removed=%d err=%v", len(result.Added), len(result.Changed), len(result.Removed), err)
	}
	current, err := store.AppByID(ctx, frontend.ID)
	if err != nil || current.RAMMB != frontend.RAMMB || !reflect.DeepEqual(current.Manifest, frontend.Manifest) {
		t.Fatalf("stage changed production configuration: err=%v", err)
	}
	current, err = store.AppByID(ctx, legacy.ID)
	if err != nil || current.Status == state.AppDeleted {
		t.Fatalf("stage removed omitted production member: status=%s err=%v", current.Status, err)
	}
	specs := store.(state.ProjectEnvironmentWorkloadSpecReader)
	spec, err := specs.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", frontend.ID)
	if err != nil || spec.Settings.RAMMB != 512 || spec.Settings.Manifest.ProjectImage != "docker.io/library/nginx:1.27" {
		t.Fatalf("stage did not retain its own source settings: err=%v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: frontend.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "docker.io/library/nginx:1.27"})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := state.AppForDeployment(ctx, store, deployment)
	if err != nil || pinned.Manifest.ProjectImage != "docker.io/library/nginx:1.27" || pinned.RAMMB != 512 {
		t.Fatalf("deployment did not pin scoped settings: err=%v", err)
	}
	scan.Workloads[0].Name, scan.Workloads[0].RenamedFrom = "renamed", "frontend"
	if _, err := service.ReconcileEnvironment(ctx, project, scan, "staging", "rename", false); err == nil {
		t.Fatal("stage renamed shared production identity")
	}
	scan.Workloads[0].Name, scan.Workloads[0].RenamedFrom = "frontend", ""
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileEnvironment(ctx, project, scan, "staging", "unapproved", false); err == nil {
		t.Fatal("stage bypassed a newly protected environment")
	}
	if _, err := service.ReconcileEnvironment(ctx, project, scan, "staging", "approved", true); err != nil {
		t.Fatalf("authorized protected stage rejected: %v", err)
	}
}
