// adr: 590
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentRouteUsesPinnedWorkloadSettings(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "route-workload-spec@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "route-spec"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "route-spec-api",
		Visibility: api.AppVisibilityInternal, MaxConcurrency: 2, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.Visibility, settings.MaxConcurrency, settings.AppProtocol = api.AppVisibilityPublic, 8, "http2"
	settings.Manifest.RequestTimeoutS = 17
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: ".gregale.dev", deploySuffix: ".gregale.dev"}
	host := gateway.BuildEnvironmentHost(".gregale.dev", env.ID, app.ID)
	resolved, found, err := router.ResolveHost(ctx, host)
	if err != nil || !found || resolved.MaxConcurrency != 8 || resolved.AppProtocol != "http2" || resolved.RequestTimeoutS != 17 {
		t.Fatalf("scoped route = %+v, found=%v, err=%v", resolved, found, err)
	}
	settings.MaxConcurrency, settings.MaintenanceMode = 9, true
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	resolved, found, err = router.ResolveHost(ctx, host)
	if err != nil || !found || resolved.MaxConcurrency != 8 || resolved.MaintenanceMode {
		t.Fatalf("stage edit changed already tested route: %+v, found=%v, err=%v", resolved, found, err)
	}
	preview, found, err := router.deploymentPreview(ctx, app.Slug, first.Revision)
	if err != nil || !found || preview.MaxConcurrency != 8 || preview.MaintenanceMode {
		t.Fatalf("qualification preview lost pinned stage settings: %+v, found=%v, err=%v", preview, found, err)
	}
	if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, state.ProjectEnvironmentRoutePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []state.DeclaredRoute{{Path: "/staging", Methods: []string{"GET"}}},
	}); err != nil {
		t.Fatal(err)
	}
	matcher := newDeclaredRoutesMatcher(store)
	checked, err := matcher.ResolveScopedRoutePolicy(ctx, resolved)
	if err != nil || checked.OnlyAllowDeclaredRoutes {
		t.Fatalf("desired route edit changed old release: %+v, %v", checked, err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	resolved, found, err = router.ResolveHost(ctx, host)
	if err != nil || !found || resolved.MaxConcurrency != 9 || !resolved.MaintenanceMode {
		t.Fatalf("new release did not apply new settings: %+v, found=%v, err=%v", resolved, found, err)
	}
	checked, err = matcher.ResolveScopedRoutePolicy(ctx, resolved)
	if err != nil || !checked.OnlyAllowDeclaredRoutes || len(checked.DeclaredRoutes) != 1 || checked.DeclaredRoutes[0].Path != "/staging" {
		t.Fatalf("new deployment did not pin route revision: %+v, %v", checked, err)
	}
	production, err := store.AppByID(ctx, app.ID)
	if err != nil || production.Visibility != api.AppVisibilityInternal || production.MaxConcurrency != 2 || production.MaintenanceMode {
		t.Fatalf("stage release changed production: %+v, %v", production, err)
	}
}
