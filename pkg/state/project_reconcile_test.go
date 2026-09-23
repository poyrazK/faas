package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreApplyProjectReconcileRollsBackOnCronResolutionError(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "project-reconcile@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "demo", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, ProjectID: project.ID, Slug: "api", WorkloadName: "api", Status: AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/", true); err != nil {
		t.Fatalf("CreateCron: %v", err)
	}

	root := "services/api"
	_, err = store.ApplyProjectReconcile(ctx, project, []ProjectReconcileMutation{
		{Op: "update", App: App{ID: app.ID, RootDir: root, WorkloadName: "api", StartCommand: "run"}},
		{Op: "create", App: App{Slug: "worker", WorkloadName: "worker", Status: AppActive}},
	}, []ProjectReconcileCron{{WorkloadName: "missing", Schedule: "0 * * * *", Path: "/", Enabled: true}}, ProjectScanSourceCompose, api.MustLimitsFor(api.PlanHobby))
	if err == nil {
		t.Fatal("expected cron resolution error")
	}

	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.RootDir != "" || got.StartCommand != "" {
		t.Fatalf("app mutation leaked after rollback: %#v", got)
	}
	if _, err := store.AppBySlug(ctx, "worker"); err == nil {
		t.Fatal("created app leaked after rollback")
	}
	crons, err := store.ListCronsForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListCronsForApp: %v", err)
	}
	if len(crons) != 1 || crons[0].Schedule != "*/5 * * * *" {
		t.Fatalf("cron mutation leaked after rollback: %#v", crons)
	}
}

func TestMemStoreApplyProjectReconcileRestoresRemovedWorkloadInPlace(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "project-restore@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "restore", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.CreateApp(ctx, App{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "worker", WorkloadName: "worker",
		RootDir: "services/worker", WorkloadClass: WorkloadClassWorker, StartCommand: "node worker.js", Status: AppActive,
		Manifest: AppManifest{WorkingDir: "/workspace", Env: map[string]string{"CUSTOM": "kept"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateCron(ctx, original.ID, "*/5 * * * *", "/job", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, original.ID); err != nil {
		t.Fatal(err)
	}

	result, err := store.ApplyProjectReconcile(ctx, project, []ProjectReconcileMutation{{
		Op: "create",
		App: App{
			Slug: "worker", WorkloadName: "worker", RootDir: "apps/worker",
			WorkloadClass: WorkloadClassJob, StartCommand: "node new-worker.js",
			Manifest: AppManifest{BuildDockerfile: "Dockerfile.worker", Env: map[string]string{"SERVICE_URL": "http://api.internal"}},
		},
	}}, nil, ProjectScanSourceCompose, api.MustLimitsFor(api.PlanHobby))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 {
		t.Fatalf("restored apps = %#v", result.Added)
	}
	restored := result.Added[0]
	if restored.ID != original.ID || restored.Status != AppActive || restored.DeletedAt != nil || restored.DeleteGraceUntil != nil {
		t.Fatalf("restored identity/lifecycle = %#v", restored)
	}
	if restored.RootDir != "apps/worker" || restored.StartCommand != "node new-worker.js" || restored.WorkloadClass != WorkloadClassJob {
		t.Fatalf("restored plan fields = %#v", restored)
	}
	if restored.Manifest.WorkingDir != "/workspace" || restored.Manifest.Env["CUSTOM"] != "kept" ||
		restored.Manifest.Env["SERVICE_URL"] == "" || restored.Manifest.BuildDockerfile != "Dockerfile.worker" {
		t.Fatalf("restored attached configuration = %#v", restored.Manifest)
	}
	crons, err := store.ListCronsForApp(ctx, restored.ID)
	if err != nil || len(crons) != 0 {
		t.Fatalf("restored crons = %#v, %v; deleted workload schedules must be declared again", crons, err)
	}
}

func TestMergeProjectManagedManifestRefreshesServiceBindingEnv(t *testing.T) {
	existing := AppManifest{
		Env: map[string]string{
			"CUSTOM":                          "kept",
			"GREGALE_SERVICE_OLD_SERVICE_URL": "http://old-service.svc.gregale:10080",
		},
		ServiceBindings: []api.AppServiceBinding{{
			Binding: "GREGALE_SERVICE_OLD_SERVICE_URL",
			Service: "old-service",
		}},
	}
	desired := AppManifest{
		Env: map[string]string{
			"GREGALE_SERVICE_API_URL": "http://api.svc.gregale:10080",
		},
		ServiceBindings: []api.AppServiceBinding{{
			Binding: "GREGALE_SERVICE_API_URL",
			Service: "api",
		}},
	}

	got := mergeProjectManagedManifest(existing, desired)
	if got.Env["CUSTOM"] != "kept" || got.Env["GREGALE_SERVICE_API_URL"] == "" {
		t.Fatalf("merged env = %#v, want custom env and current service binding", got.Env)
	}
	if _, ok := got.Env["GREGALE_SERVICE_OLD_SERVICE_URL"]; ok {
		t.Fatalf("merged env = %#v, stale service binding was retained", got.Env)
	}
	if len(got.ServiceBindings) != 1 || got.ServiceBindings[0] != desired.ServiceBindings[0] {
		t.Fatalf("service bindings = %#v, want %#v", got.ServiceBindings, desired.ServiceBindings)
	}
}
