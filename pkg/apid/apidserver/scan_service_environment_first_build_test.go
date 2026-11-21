package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us hunt #9: `gregale env create staging --from production`
// followed by `gregale deploy --project --environment staging` queued no
// builds, because the reconcile left every app row unchanged, and the CLI
// reported all three builds as failed. An app that has never been deployed
// to the requested environment must build there.
func TestEnvironmentAppsWithoutLiveDeployment(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "env-first@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	deployedInStaging, neverInStaging, alreadyChanged := mk("back"), mk("front"), mk("rogue")
	for _, app := range []state.App{deployedInStaging, neverInStaging, alreadyChanged} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
	}
	staging, err := store.CreateDeployment(ctx, state.Deployment{AppID: deployedInStaging.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending, Scope: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, staging.ID); err != nil {
		t.Fatal(err)
	}

	got, err := apidserver.EnvironmentAppsWithoutLiveDeployment(ctx, store,
		[]state.App{deployedInStaging, neverInStaging, alreadyChanged}, "staging", nil, []state.App{alreadyChanged})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != neverInStaging.ID {
		t.Fatalf("first builds = %+v, want only %s (live in production, never in staging)", got, neverInStaging.Slug)
	}
	for _, env := range []string{"", "default", "production"} {
		if !apidserver.ReconcileProductionEnvironment(env) {
			t.Errorf("reconcileProductionEnvironment(%q) = false; unchanged production apps must not rebuild", env)
		}
	}
	if apidserver.ReconcileProductionEnvironment("staging") {
		t.Error("staging treated as the production scope")
	}
}

// 2026-11-15 agent eval: three retail services failed their first boot, and
// every later `gregale deploy --project` reported them "unchanged; its live
// release keeps serving" and queued nothing. A production app with no live
// release whose last deployment ended (or that never deployed) rebuilds; one
// still deploying, one already serving and one already queued do not.
func TestProductionAppsWithoutRelease(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "prod-down@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	deploy := func(app state.App) state.Deployment {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	serving, failed, building, never, queued := mk("ui"), mk("orders"), mk("cart"), mk("catalog"), mk("checkout")
	if err := store.MarkDeploymentLive(ctx, deploy(serving).ID); err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{failed, queued} {
		if _, err := store.SetDeploymentFailed(ctx, deploy(app).ID, "internal", "cold boot failed"); err != nil {
			t.Fatal(err)
		}
	}
	deploy(building)

	got, err := apidserver.ProductionAppsWithoutRelease(ctx, store,
		[]state.App{serving, failed, building, never, queued}, nil, []state.App{queued})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{failed.ID: true, never.ID: true}
	if len(got) != len(want) {
		t.Fatalf("rebuilds = %+v, want %s and %s", got, failed.Slug, never.Slug)
	}
	for _, app := range got {
		if !want[app.ID] {
			t.Fatalf("rebuilds include %s; want only %s and %s", app.Slug, failed.Slug, never.Slug)
		}
	}
}
