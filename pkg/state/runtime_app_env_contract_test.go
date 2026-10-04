// adr: 567
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeAppEnvTestStore interface {
	state.Store
	state.RuntimeAppEnvStore
	state.ProjectEnvironmentWorkloadSpecStore
}

type runtimeAppEnvFixture struct {
	account     state.Account
	project     state.Project
	app         state.App
	deployments map[string]state.Deployment
}

func seedRuntimeAppEnv(t *testing.T, store runtimeAppEnvTestStore) runtimeAppEnvFixture {
	t.Helper()
	ctx := t.Context()
	f := runtimeAppEnvFixture{deployments: map[string]state.Deployment{}}
	var err error
	f.account, err = store.CreateAccount(ctx, "runtime-env-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "runtime-env"})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID,
		Slug: "runtime-env-" + uuid.NewString(), Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "stage", "other"} {
		if _, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, scope); errors.Is(err, state.ErrNotFound) {
			if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: scope}); err != nil {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, scope, f.app.ID, 0, settings); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []string{"default", "production", "stage", "other"} {
		if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, scope, "MODE", scope); err != nil {
			t.Fatal(err)
		}
		f.deployments[scope], err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestMemRuntimeAppEnvDeploymentScopeAndOwnership(t *testing.T) {
	testRuntimeAppEnvDeploymentScopeAndOwnership(t, state.NewMemStore())
}

func testRuntimeAppEnvDeploymentScopeAndOwnership(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	for _, scope := range []string{"default", "production", "stage", "other"} {
		dep := f.deployments[scope]
		got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
		environmentScope := scope
		if scope == "default" {
			environmentScope = "production"
		}
		env, envErr := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, environmentScope)
		if err != nil || envErr != nil || got.AccountID != f.account.ID || got.AppID != f.app.ID || got.DeploymentID != dep.ID ||
			got.Scope != scope || got.EnvironmentID != env.ID || len(got.Values) != 1 || got.Values[0].Scope != scope || got.Values[0].Value != scope {
			t.Fatalf("scope %s: %+v %v environment=%+v %v", scope, got, err, env, envErr)
		}
		got.Values[0].Value = "caller-mutated"
		again, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
		if err != nil || again.Values[0].Value != scope {
			t.Fatalf("caller changed stored values: %+v %v", again, err)
		}
	}
	for _, identity := range []struct{ accountID, appID, deploymentID string }{
		{uuid.NewString(), f.app.ID, f.deployments["stage"].ID},
		{f.account.ID, uuid.NewString(), f.deployments["stage"].ID},
		{f.account.ID, f.app.ID, uuid.NewString()},
	} {
		if got, err := store.RuntimeAppEnvForDeployment(ctx, identity.accountID, identity.appID, identity.deploymentID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
			t.Fatalf("wrong owner read values: %+v %v", got, err)
		}
	}
	if _, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, "malformed"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid identity: %v", err)
	}
	if err := store.DeleteAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE"); err != nil {
		t.Fatal(err)
	}
	got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, f.deployments["stage"].ID)
	if err != nil || got.Values == nil || len(got.Values) != 0 {
		t.Fatalf("empty stage fell back to production: %+v %v", got, err)
	}
	if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "edited"); err != nil {
		t.Fatal(err)
	}
	got, err = store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, f.deployments["stage"].ID)
	if err != nil || got.Values[0].Value != "edited" {
		t.Fatalf("edit invisible: %+v %v", got, err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployFailed, state.DeployCancelled} {
		if err := store.UpdateDeploymentStatus(ctx, f.deployments["stage"].ID, status, "test"); err != nil {
			t.Fatal(err)
		}
		if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, f.deployments["stage"].ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
			t.Fatalf("terminal deployment read values: %+v %v", got, err)
		}
	}
	if err := store.DeleteApp(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, f.deployments["other"].ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
		t.Fatalf("deleted app read values: %+v %v", got, err)
	}
}

func TestMemRuntimeAppEnvStageLifetimeAndLegacy(t *testing.T) {
	testRuntimeAppEnvStageLifetimeAndLegacy(t, state.NewMemStore())
}

func testRuntimeAppEnvStageLifetimeAndLegacy(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	dep := f.deployments["stage"]
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); err != nil {
		t.Fatalf("draining pinned deployment: %v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted environment read: %v", err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "replacement-private"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
		t.Fatalf("old VM adopted replacement stage: %+v %v", got, err)
	}
	// Deployments made before settings pinning retain strictly scoped reads.
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "legacy"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "legacy", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "legacy", "MODE", "legacy-only"); err != nil {
		t.Fatal(err)
	}
	got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, legacy.ID)
	if err != nil || len(got.Values) != 1 || got.Values[0].Value != "legacy-only" {
		t.Fatalf("legacy scoped read: %+v %v", got, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, legacy.ID); !errors.Is(err, state.ErrNotFound) || len(got.Values) != 0 {
		t.Fatalf("legacy VM adopted replacement stage: %+v %v", got, err)
	}
}
