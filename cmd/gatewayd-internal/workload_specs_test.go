// adr: 590
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
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

func TestProductionRouteHonorsDeployedSettings(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "regression-prod-pin@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "regression-prod-pin"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "regression-prod-pin-app", Type: state.AppTypeApp, Visibility: api.AppVisibilityPublic, RAMMB: 256, MaxConcurrency: 2, Status: state.AppActive, PublicAuthMode: api.AppPublicAuthModeOpen,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 600}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "production", false)
	if err != nil {
		t.Fatal(err)
	}
	// The default deployment predates settings pins. A dark named production
	// candidate must not change its ordinary ingress policy.
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "pinned.example.test", app.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "pinned.example.test"); err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: ".gregale.dev", deploySuffix: ".gregale.dev"}
	hosts := []string{app.Slug + ".gregale.dev", "pinned.example.test"}
	assertPolicy := func(authn, maintenance bool) {
		t.Helper()
		for _, host := range hosts {
			resolved, found, err := router.ResolveHost(ctx, host)
			if err != nil || !found || resolved.RequireAuthn != authn || resolved.MaintenanceMode != maintenance || resolved.PinnedDeploymentID != "" {
				t.Fatalf("host %s: authn=%v maintenance=%v pin=%q found=%v err=%v", host, resolved.RequireAuthn, resolved.MaintenanceMode, resolved.PinnedDeploymentID, found, err)
			}
		}
	}
	enabled := true
	_, err = state.UpdateEnvironmentWorkloadSettings(ctx, store, app, env, nil, state.UpdateAppParams{RequireAuthn: &enabled, SetRequireAuthn: true, MaintenanceMode: &enabled, SetMaintenanceMode: true})
	if err != nil {
		t.Fatal(err)
	}
	assertPolicy(false, false)
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployLive, TrafficPercentExplicit: true}); err != nil {
		t.Fatal(err)
	}
	assertPolicy(false, false)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	exact, found, err := router.ResolveHost(ctx, gateway.BuildEnvironmentHost(".gregale.dev", env.ID, app.ID))
	if err != nil || !found || !exact.RequireAuthn || !exact.MaintenanceMode {
		t.Fatalf("environment route: %+v found=%v err=%v", exact, found, err)
	}
	assertPolicy(true, true)
	backend := gateway.NewPGBackend(router, nil, testLogger()).WithStore(weightsStoreAdapter{store: store})
	if cached, found := backend.Lookup(ctx, hosts[0]); !found || !cached.RequireAuthn || !cached.MaintenanceMode {
		t.Fatal("initial cached ingress lost deployed settings")
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 600, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
		t.Fatal(err)
	}
	enabled = false
	if _, err := state.UpdateEnvironmentWorkloadSettings(ctx, store, app, env, nil, state.UpdateAppParams{RequireAuthn: &enabled, SetRequireAuthn: true, MaintenanceMode: &enabled, SetMaintenanceMode: true}); err != nil {
		t.Fatal(err)
	}
	assertPolicy(true, true)
	next, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	handleInvalidation(ctx, backend, db.Notification{Channel: db.NotifyDeploymentChanged, Payload: `{"app_id":"` + app.ID + `"}`}, testLogger())
	assertPolicy(true, true)
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 600, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}}); err != nil {
		t.Fatal(err)
	}
	handleInvalidation(ctx, backend, db.Notification{Channel: db.NotifyAppChanged, Payload: app.ID}, testLogger())
	if cached, found := backend.Lookup(ctx, hosts[0]); !found || cached.RequireAuthn || cached.MaintenanceMode {
		t.Fatalf("deployment cutover retained cached settings: found=%v authn=%v maintenance=%v", found, cached.RequireAuthn, cached.MaintenanceMode)
	}
	assertPolicy(false, false)
	// Visibility belongs to the deployed revision too. A raw production
	// projection edit must not hide a still-public pinned release.
	visibility := api.AppVisibilityInternal
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Visibility: &visibility, SetVisibility: true}); err != nil {
		t.Fatal(err)
	}
	assertPolicy(false, false)
	if _, err := state.UpdateEnvironmentWorkloadSettings(ctx, store, app, env, nil, state.UpdateAppParams{Visibility: &visibility, SetVisibility: true}); err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployLive, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 600, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: private.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, host := range hosts {
		if _, found, err := router.ResolveHost(ctx, host); err != nil || found {
			t.Fatalf("private production host %s: found=%v err=%v", host, found, err)
		}
	}
}
