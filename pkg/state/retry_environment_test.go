package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type retryEnvironmentFixture struct {
	account state.Account
	project state.Project
	app     state.App
	env     state.ProjectEnvironment
	spec    state.ProjectEnvironmentWorkloadSpec
	dep     state.Deployment
}

func seedRetryEnvironment(t *testing.T, store contractTestStore, ctx context.Context) retryEnvironmentFixture {
	t.Helper()
	account, err := store.CreateAccount(ctx, "retry-environment@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "retry-environment"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "retry-environment", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, StartCommand: "./production"})
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
	settings.StartCommand, settings.RAMMB = "./staging", 512
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, env.Slug, app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:retry", Scope: env.Slug})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailSourceDeployment(ctx, dep.ID, "image preparation failed"); err != nil {
		t.Fatal(err)
	}
	return retryEnvironmentFixture{account, project, app, env, spec, dep}
}

func TestRetryPreservesEnvironmentPinMem(t *testing.T) {
	testRetryPreservesEnvironmentPin(t, state.NewMemStore(), t.Context())
}

func testRetryPreservesEnvironmentPin(t *testing.T, store contractTestStore, ctx context.Context) {
	t.Helper()
	f := seedRetryEnvironment(t, store, ctx)
	// A retry must keep the failed attempt's revision, even after both the
	// desired environment head and the global app have been edited.
	next := f.spec.Settings
	next.StartCommand, next.RAMMB = "./next-staging", 1024
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, f.env.Slug, f.app.ID, f.spec.Revision, next); err != nil {
		t.Fatal(err)
	}
	command := "./next-production"
	if _, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{StartCommand: &command}); err != nil {
		t.Fatal(err)
	}
	retry, err := store.RetryDeploymentFromStage(ctx, f.dep.ID, state.StageImageBuild)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, f.account.ID, f.project.ID, retry.ID)
	if err != nil || pin.ID != f.spec.ID {
		t.Fatalf("retry pin=%s want=%s err=%v", pin.ID, f.spec.ID, err)
	}
	resolved, err := state.AppForDeployment(ctx, store, retry)
	if err != nil || resolved.StartCommand != "./staging" || resolved.RAMMB != 512 {
		t.Fatalf("retry settings: command=%q RAM=%d err=%v", resolved.StartCommand, resolved.RAMMB, err)
	}
	values, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, retry.ID)
	if err != nil || values.EnvironmentID != f.env.ID {
		t.Fatalf("retry environment owner=%s want=%s err=%v", values.EnvironmentID, f.env.ID, err)
	}
}

func TestRetryPreservesEnvironmentLifetimeMem(t *testing.T) {
	testRetryPreservesEnvironmentLifetime(t, state.NewMemStore(), t.Context())
}

func testRetryPreservesEnvironmentLifetime(t *testing.T, store contractTestStore, ctx context.Context) {
	t.Helper()
	f := seedRetryEnvironment(t, store, ctx)
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, f.env.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: f.env.Slug}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, f.env.Slug, f.app.ID, 0, f.spec.Settings); err != nil {
		t.Fatal(err)
	}
	retry, err := store.RetryDeploymentFromStage(ctx, f.dep.ID, state.StageImageBuild)
	if err != nil {
		t.Fatal(err)
	}
	if values, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, retry.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retry acquired replacement environment: %+v err=%v", values, err)
	}
}

func TestRetryPreservesStartupCPUBoostMem(t *testing.T) {
	testRetryPreservesStartupCPUBoost(t, state.NewMemStore(), t.Context())
}

func testRetryPreservesStartupCPUBoost(t *testing.T, store contractTestStore, ctx context.Context) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "retry-cpu@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "retry-cpu", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, disabled := range []bool{false, true} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:retry-cpu", DisableStartupCPUBoost: disabled})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FailSourceDeployment(ctx, dep.ID, "image preparation failed"); err != nil {
			t.Fatal(err)
		}
		retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild)
		if err != nil {
			t.Fatal(err)
		}
		if retry.DisableStartupCPUBoost != disabled {
			t.Fatalf("retry CPU boost opt-out=%v want=%v", retry.DisableStartupCPUBoost, disabled)
		}
	}
}
