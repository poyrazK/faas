package apidserver

import (
	"context"
	"testing"

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

	got, err := environmentAppsWithoutLiveDeployment(ctx, store,
		[]state.App{deployedInStaging, neverInStaging, alreadyChanged}, "staging", nil, []state.App{alreadyChanged})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != neverInStaging.ID {
		t.Fatalf("first builds = %+v, want only %s (live in production, never in staging)", got, neverInStaging.Slug)
	}
	for _, env := range []string{"", "default", "production"} {
		if !reconcileProductionEnvironment(env) {
			t.Errorf("reconcileProductionEnvironment(%q) = false; unchanged production apps must not rebuild", env)
		}
	}
	if reconcileProductionEnvironment("staging") {
		t.Error("staging treated as the production scope")
	}
}
