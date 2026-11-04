package reconcile_test

import (
	"context"
	"errors"
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

func productionReconcileFixture(t *testing.T, store state.Store) (state.Project, state.App, *reconcile.Service) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "production-reconcile@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "production-shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "production-web", WorkloadName: "web",
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP, RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 2,
		Manifest: state.AppManifest{ProjectImage: "docker.io/library/nginx:1.26", ProjectImagePort: 80, ProjectSourceSHA256: "accepted-production"}})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return project, app, reconcile.NewService(store, audit.New(store, log, nil, "reconcile"), log)
}

func productionScan(name string, ram, port int) reposcan.Result {
	return reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{Name: name, Image: "docker.io/library/nginx:1.27",
		Ports: []int{port}, Class: reposcan.ClassHTTP, SourceSHA256: "new-source", Tier: reposcan.TierCompose,
		Source: "compose.yaml: " + name, DetectedBy: reposcan.Detection{Detector: reposcan.DetectorCompose},
		Resources: &api.ComposeResources{MemoryMB: ram}}}}
}

func TestMemProductionReconcilePinsCurrentSettings(t *testing.T) {
	testProductionReconcilePinsCurrentSettings(t, state.NewMemStore())
}

func testProductionReconcilePinsCurrentSettings(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	project, app, service := productionReconcileFixture(t, store)
	specs := store.(state.ProjectEnvironmentWorkloadSpecStore)
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WorkPolicies = &state.ProjectEnvironmentWorkPolicySettings{Revision: 1}
	settings.QueueBindings = &state.ProjectEnvironmentQueueSettings{Revision: 1}
	baseline, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "production", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: app.Manifest.ProjectImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: project.AccountID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	stageSettings := settings
	stageSettings.RAMMB = 768
	stage, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "staging", app.ID, 0, stageSettings)
	if err != nil {
		t.Fatal(err)
	}
	scan := productionScan("web", 512, 8080)
	result, err := service.Reconcile(ctx, project, scan, "new", "", nil)
	if err != nil || len(result.Changed) != 1 {
		t.Fatalf("reconcile: %+v %v", result, err)
	}
	head, err := specs.ProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "production", app.ID)
	if err != nil || head.Revision != baseline.Revision+1 || head.Settings.RAMMB != 512 || head.Settings.Manifest.ProjectImagePort != 8080 ||
		!reflect.DeepEqual(head.Settings.WorkPolicies, baseline.Settings.WorkPolicies) || !reflect.DeepEqual(head.Settings.QueueBindings, baseline.Settings.QueueBindings) {
		t.Fatalf("production settings did not advance or lost policies: %+v %v", head, err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: scan.Workloads[0].Image})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := state.AppForDeployment(ctx, store, dep)
	if err != nil || pinned.RAMMB != 512 || pinned.Manifest.ProjectImagePort != 8080 {
		t.Fatalf("new deployment pinned stale settings: %+v %v", pinned, err)
	}
	prior, err := state.AppForDeployment(ctx, store, old)
	if err != nil || prior.RAMMB != 256 || prior.Manifest.ProjectImagePort != 80 {
		t.Fatalf("old deployment settings changed: %+v %v", prior, err)
	}
	manifest := result.Changed[0].Manifest
	manifest.ProjectSourceSHA256 = scan.Workloads[0].SourceSHA256
	checkpoint, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil || checkpoint.RAMMB != 512 {
		t.Fatalf("checkpoint reverted resources: ram=%d err=%v", checkpoint.RAMMB, err)
	}
	before, err := specs.ProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "production", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReconcileWithCrons(ctx, project, productionScan("web", 1024, 9000), "bad", "", nil,
		[]reconcile.CronSpec{{WorkloadName: "missing", Schedule: "0 * * * *", Path: "/"}})
	if err == nil {
		t.Fatal("invalid cron should roll back the transaction")
	}
	after, err := specs.ProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "production", app.ID)
	if err != nil || after.ID != before.ID {
		t.Fatalf("failed transaction advanced settings: %s => %s %v", before.ID, after.ID, err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || current.RAMMB != 512 || current.Manifest.ProjectImagePort != 8080 {
		t.Fatalf("failed transaction changed the app: %+v %v", current, err)
	}
	stageAfter, err := specs.ProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "staging", app.ID)
	if err != nil || stageAfter.ID != stage.ID {
		t.Fatal("production reconcile changed staging", err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(ctx, project, productionScan("web", 768, 9000), "restore", "", nil); err != nil {
		t.Fatal(err)
	}
	restored, err := specs.ProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, "production", app.ID)
	if err != nil || restored.Settings.RAMMB != 768 || restored.Settings.Manifest.ProjectImagePort != 9000 {
		t.Fatalf("restored app retained stale settings: %+v %v", restored, err)
	}
}

func TestMemProductionReconcilePreservesEnvironmentOnlyServices(t *testing.T) {
	testProductionReconcilePreservesEnvironmentOnlyServices(t, state.NewMemStore())
}

func testProductionReconcilePreservesEnvironmentOnlyServices(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	project, _, service := productionReconcileFixture(t, store)
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: project.AccountID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	staged, err := service.ReconcileEnvironment(ctx, project, productionScan("experimental", 256, 80), "staging", "stage", false)
	if err != nil || len(staged.Added) != 1 {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	app := staged.Added[0]
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: app.Manifest.ProjectImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	production := productionScan("web", 256, 80)
	plan, err := service.Plan(ctx, project, production, "main", "", nil)
	if err != nil || len(plan.Removed) != 0 {
		t.Fatalf("plan removes stage-only member: %+v %v", plan, err)
	}
	_, err = store.(state.ProjectReconcileStore).ApplyProjectReconcile(ctx, project,
		[]state.ProjectReconcileMutation{{Op: "remove", App: app}}, nil, project.ScanSource, api.MustLimitsFor(api.PlanPro))
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("store did not fence stale removal: %v", err)
	}
	result, err := service.Reconcile(ctx, project, production, "main", "", nil)
	if err != nil || len(result.Removed) != 0 {
		t.Fatalf("production removed stage-only member: %+v %v", result, err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || current.Status == state.AppDeleted {
		t.Fatalf("stage-only app deleted: %+v %v", current, err)
	}
	if _, err := state.AppForDeployment(ctx, store, dep); err != nil {
		t.Fatal("staging settings lost", err)
	}
	// Explicitly selecting it for production establishes production membership.
	production.Workloads = append(production.Workloads, productionScan("experimental", 512, 80).Workloads[0])
	if _, err := service.Reconcile(ctx, project, production, "admit", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: app.Manifest.ProjectImage}); err != nil {
		t.Fatal(err)
	}
	result, err = service.Reconcile(ctx, project, productionScan("web", 256, 80), "remove", "", nil)
	if err != nil || len(result.Removed) != 1 || result.Removed[0] != app.ID {
		t.Fatalf("explicit production membership lost removal policy: %+v %v", result, err)
	}
}
