package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicURLTestStore interface {
	state.PublicBuildURLStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	SetDeploymentFailed(context.Context, string, string, string) (state.Deployment, error)
	RetryDeploymentFromStage(context.Context, string, state.StageName) (state.Deployment, error)
}

func testPublicBuildURLSnapshots(t *testing.T, s publicURLTestStore) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "web-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(scope string, kind state.DeploymentKind) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind, Scope: scope, SourcePath: "/tmp/public-source.tar.gz", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	legacy := deploy(api.DefaultEnvScope, state.DeploymentKindTarball)
	urls := map[string]string{"VITE_API_URL": "https://api.example.com/v1"}
	if err := s.SetAppPublicBuildURLs(ctx, uuid.NewString(), app.ID, urls); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account wrote build configuration", err)
	}
	if err := s.SetAppPublicBuildURLs(ctx, a.ID, app.ID, urls); err != nil {
		t.Fatal(err)
	}
	urls["VITE_API_URL"] = "https://mutated.example.com"
	first := deploy(api.DefaultEnvScope, state.DeploymentKindTarball)
	got, err := s.DeploymentPublicBuildURLs(ctx, first.ID)
	if err != nil || got["VITE_API_URL"] != "https://api.example.com/v1" {
		t.Fatalf("snapshot: %v %v", got, err)
	}
	got["VITE_API_URL"] = "https://reader-mutated.example.com"
	if err := s.SetAppPublicBuildURLs(ctx, a.ID, app.ID, map[string]string{"VITE_API_URL": "https://new-api.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.DeploymentPublicBuildURLs(ctx, first.ID)
	if err != nil || got["VITE_API_URL"] != "https://api.example.com/v1" {
		t.Fatal("queued snapshot changed with app intent or a reader")
	}
	got, err = s.DeploymentPublicBuildURLs(ctx, legacy.ID)
	if err != nil || len(got) != 0 {
		t.Fatal("older deployment adopted new app configuration")
	}
	if _, err := s.SetDeploymentFailed(ctx, first.ID, "fixture_failure", "fixture failure"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryDeploymentFromStage(ctx, first.ID, state.StageSourceDownload)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.DeploymentPublicBuildURLs(ctx, retry.ID)
	if err != nil || got["VITE_API_URL"] != "https://api.example.com/v1" {
		t.Fatal("retry adopted changed app configuration")
	}
	fresh := deploy(api.DefaultEnvScope, state.DeploymentKindGitHub)
	got, err = s.DeploymentPublicBuildURLs(ctx, fresh.ID)
	if err != nil || got["VITE_API_URL"] != "https://new-api.example.com" {
		t.Fatal("new deployment missed updated public configuration")
	}
	for _, d := range []state.Deployment{deploy("staging", state.DeploymentKindTarball), deploy(api.DefaultEnvScope, state.DeploymentKindPreview)} {
		got, err = s.DeploymentPublicBuildURLs(ctx, d.ID)
		if err != nil || len(got) != 0 {
			t.Fatal("nonproduction deployment inherited production URL")
		}
	}
	if err := s.SetAppPublicBuildURLs(ctx, a.ID, app.ID, map[string]string{"DATABASE_URL": "https://private.example.com"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("secret key entered public configuration")
	}
}

func TestMemPublicBuildURLSnapshots(t *testing.T) {
	testPublicBuildURLSnapshots(t, state.NewMemStore())
}
