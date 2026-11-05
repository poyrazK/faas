package reconcile_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/serviceadditions"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemReleaseMembershipIsEnvironmentScoped(t *testing.T) {
	testReleaseMembershipIsEnvironmentScoped(t, state.NewMemStore())
}

func testReleaseMembershipIsEnvironmentScoped(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "release-membership@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "release-shop"})
	if err != nil {
		t.Fatal(err)
	}
	makeApp := func(name string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp,
			WorkloadClass: state.WorkloadClassHTTP, RAMMB: 256, CPUMillicores: 500,
			Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	deploy := func(app state.App, scope string) state.ProjectReleaseMember {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return state.ProjectReleaseMember{AppID: app.ID, DeploymentID: d.ID}
	}
	web := makeApp("release-web")
	webProduction := deploy(web, "production")
	graphs := store.(state.ProjectReleaseSetStore)
	baseline, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{webProduction})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "qa"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	specs := store.(state.ProjectEnvironmentWorkloadSpecStore)
	setStage := func(app state.App, scope string) {
		t.Helper()
		settings, err := state.WorkloadSettingsFromApp(app)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, scope, app.ID, 0, settings); err != nil {
			t.Fatal(err)
		}
	}
	experiment, qaOnly := makeApp("release-experiment"), makeApp("release-qa")
	setStage(experiment, "staging")
	setStage(qaOnly, "qa")
	webStage, experimentStage := deploy(web, "staging"), deploy(experiment, "staging")
	apps, err := store.AppsForProject(ctx, account.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		scope string
		count int
	}{{"production", 1}, {api.DefaultEnvScope, 1}, {"staging", 2}, {"qa", 1}} {
		selected, err := state.ProjectAppsForEnvironment(ctx, store, account.ID, project.ID, tc.scope, apps)
		if err != nil || len(selected) != tc.count {
			t.Fatalf("membership %s: %d %v", tc.scope, len(selected), err)
		}
	}
	scan := reposcan.Result{Workloads: []reposcan.Workload{{Name: web.WorkloadName, Class: reposcan.ClassHTTP, SourceSHA256: "new"}}}
	safe := &api.ProjectSafeRelease{PreviousReleaseID: baseline.ID, TTLSeconds: 1800}
	selected, err := serviceadditions.Plan(ctx, store, account, project, safe, apps, scan)
	if err != nil || len(selected) != 1 || selected[0].ID != web.ID {
		t.Fatalf("stage app blocked safe release: %v", err)
	}
	second, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{webProduction})
	if err != nil {
		t.Fatal("stage app blocked production graph", err)
	}
	if _, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{webStage}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("omitted a staging member: %v", err)
	}
	if _, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{webStage, experimentStage}); err != nil {
		t.Fatal("QA app blocked staging graph", err)
	}
	if err := store.(state.ProjectReleaseSetPromotionStore).DeactivateProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", second.ID, []state.ProjectReleaseMember{webProduction}); err != nil {
		t.Fatal("other environments blocked production fallback", err)
	}
}
