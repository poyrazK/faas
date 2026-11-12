package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Project deploys store production rows as scope "default" while named
// production is dark. The production environment URL that `environment get`
// prints must still serve them (Hunt 34 H34-27), and a traffic-bearing named
// production row wins over the legacy lane.
func TestPgRouter_ProductionEnvironmentHostServesLegacyDefaultLane(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "production-env-host@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-web", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: ".gregale.dev", deploySuffix: ".gregale.dev"}
	host := gateway.BuildEnvironmentHost(".gregale.dev", production.ID, app.ID)

	legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Status: state.DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok, err := router.ResolveHost(ctx, host)
	if err != nil || !ok || resolved.PinnedDeploymentID != legacy.ID || resolved.PinnedDeploymentScope != state.DefaultEnvScope {
		t.Fatalf("production environment URL over the default lane = %+v ok=%v err=%v, want deployment %s in scope %q", resolved, ok, err, legacy.ID, state.DefaultEnvScope)
	}

	// A dark named production row does not take over from serving legacy.
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployLive, TrafficPercentExplicit: true}); err != nil {
		t.Fatal(err)
	}
	resolved, ok, err = router.ResolveHost(ctx, host)
	if err != nil || !ok || resolved.PinnedDeploymentID != legacy.ID {
		t.Fatalf("dark named production took the URL: %+v ok=%v err=%v", resolved, ok, err)
	}

	named, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok, err = router.ResolveHost(ctx, host)
	if err != nil || !ok || resolved.PinnedDeploymentID != named.ID || resolved.PinnedDeploymentScope != "production" {
		t.Fatalf("traffic-bearing named production = %+v ok=%v err=%v, want deployment %s", resolved, ok, err, named.ID)
	}
}
