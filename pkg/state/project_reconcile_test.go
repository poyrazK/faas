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
