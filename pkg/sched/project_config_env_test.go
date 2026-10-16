// adr: 1136
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A project environment's configuration reaches the guest's API env below the
// app's own variables, and stays out of the runtime receipt inputs, which
// freshness compares with app_envs exactly.
func TestRuntimeValuesDeliverProjectConfigBelowAppVariables(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "project-config-env@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "project-config-env"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "project-config-env-api",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "default", "LOG_LEVEL", "debug"); err != nil {
		t.Fatal(err)
	}
	values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"LOG_LEVEL":"info","ORDERS_URL":"http://orders.svc.gregale","region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", ConfigHash: hash, Values: values,
	}); err != nil {
		t.Fatal(err)
	}

	engine := &Engine{store: store}
	loaded, err := engine.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		t.Fatalf("loadRuntimeDeploymentValues: %v", err)
	}
	delivered := map[string]string{}
	for _, entry := range loaded.APIEnv {
		if _, duplicate := delivered[entry.Key]; duplicate {
			t.Fatalf("API env delivers %s twice: %+v", entry.Key, loaded.APIEnv)
		}
		delivered[entry.Key] = entry.Value
	}
	want := map[string]string{"LOG_LEVEL": "debug", "ORDERS_URL": "http://orders.svc.gregale"}
	if len(delivered) != len(want) || delivered["LOG_LEVEL"] != want["LOG_LEVEL"] || delivered["ORDERS_URL"] != want["ORDERS_URL"] {
		t.Fatalf("API env = %v, want %v (the app's own LOG_LEVEL wins, document-only keys stay out)", delivered, want)
	}
	if len(loaded.Inputs.Variables) != 1 || loaded.Inputs.Variables["LOG_LEVEL"] != "debug" {
		t.Fatalf("receipt variables = %v, want only the app's own rows", loaded.Inputs.Variables)
	}
}
