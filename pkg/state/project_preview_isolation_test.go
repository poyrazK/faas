package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreProjectReconcileIgnoresPreviewApps(t *testing.T) {
	testProjectReconcileIgnoresPreviewApps(t, state.NewMemStore())
}

// The production project membership and reconcile paths share one contract.
// A PR preview has the same project/workload identity as its parent for
// service routing, but must never become a production reconcile candidate.
func testProjectReconcileIgnoresPreviewApps(t *testing.T, store interface {
	state.Store
	state.ProjectReconcileStore
}) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "project-preview-isolation@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{
		AccountID: account.ID, Slug: "project-preview-isolation", ScanSource: state.ProjectScanSourceCompose,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	create := func(slug, workload, previewOf string, prNumber int) state.App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, state.App{
			AccountID: account.ID, ProjectID: project.ID, Slug: slug,
			WorkloadName: workload, PreviewOfSlug: previewOf, PreviewPrNumber: prNumber,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
		})
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", slug, createErr)
		}
		return app
	}
	production := create("preview-isolation-api", "api", "", 0)
	preview := create("pr-42-preview-isolation-api", "api", production.Slug, 42)

	members, err := store.AppsForProject(ctx, account.ID, project.ID)
	if err != nil || len(members) != 1 || members[0].ID != production.ID {
		t.Fatalf("AppsForProject = (%+v, %v), want only production api", members, err)
	}

	production.RootDir = "apps/api"
	result, err := store.ApplyProjectReconcile(ctx, project, []state.ProjectReconcileMutation{
		{Op: "update", App: production},
	}, []state.ProjectReconcileCron{{WorkloadName: "api", Schedule: "*/5 * * * *", Path: "/tick", Enabled: true}},
		state.ProjectScanSourceCompose, api.MustLimitsFor(api.PlanPro))
	if err != nil || len(result.Changed) != 1 || result.Changed[0].ID != production.ID {
		t.Fatalf("ApplyProjectReconcile = (%+v, %v), want production update", result, err)
	}
	gotPreview, err := store.AppByID(ctx, preview.ID)
	if err != nil || gotPreview.RootDir != "" || gotPreview.Status != state.AppActive {
		t.Fatalf("preview changed by production reconcile = (%+v, %v)", gotPreview, err)
	}
	productionCrons, err := store.ListCronsForApp(ctx, production.ID)
	if err != nil || len(productionCrons) != 1 {
		t.Fatalf("production crons = (%+v, %v), want one", productionCrons, err)
	}
	previewCrons, err := store.ListCronsForApp(ctx, preview.ID)
	if err != nil || len(previewCrons) != 0 {
		t.Fatalf("preview crons = (%+v, %v), want none", previewCrons, err)
	}

	for _, op := range []string{"update", "remove"} {
		if _, err := store.ApplyProjectReconcile(ctx, project,
			[]state.ProjectReconcileMutation{{Op: op, App: preview}}, nil,
			state.ProjectScanSourceCompose, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("direct %s of preview = %v, want ErrNotFound", op, err)
		}
	}

	// A deleted preview is not a production tombstone. A new production
	// workload with that name must receive a fresh production app row.
	deletedPreview := create("pr-42-preview-isolation-worker", "worker", production.Slug, 42)
	if _, err := store.SoftDeleteAppCascade(ctx, deletedPreview.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade(preview): %v", err)
	}
	created, err := store.ApplyProjectReconcile(ctx, project,
		[]state.ProjectReconcileMutation{{Op: "create", App: state.App{
			Slug: "preview-isolation-worker", WorkloadName: "worker", Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
		}}}, nil, state.ProjectScanSourceCompose, api.MustLimitsFor(api.PlanPro))
	if err != nil || len(created.Added) != 1 {
		t.Fatalf("create production worker = (%+v, %v)", created, err)
	}
	if created.Added[0].ID == deletedPreview.ID || created.Added[0].PreviewOfSlug != "" {
		t.Fatalf("production worker reused preview tombstone: %+v", created.Added[0])
	}
}
