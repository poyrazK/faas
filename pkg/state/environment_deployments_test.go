// adr: 566
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemEnvironmentDeploymentSelection(t *testing.T) {
	testEnvironmentDeploymentSelection(t, state.NewMemStore())
}

type unavailableEnvironmentGraph struct{ state.Store }

func (s unavailableEnvironmentGraph) ResolveProjectRelease(context.Context, string, string, string) (string, string, error) {
	return "", "", state.ErrConflict
}

func testEnvironmentDeploymentSelection(t *testing.T, store runtimeAppEnvTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	check := func(scope, want string) {
		t.Helper()
		got, err := state.ResolveEnvironmentDeployment(ctx, store, f.app.ID, scope)
		if want == "" {
			if !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing environment selected deployment: %+v %v", got, err)
			}
		} else if err != nil || got.ID != want {
			t.Fatalf("environment %s selected %+v %v; want %s", scope, got, err, want)
		}
	}
	if err := store.MarkDeploymentLive(ctx, f.deployments["production"].ID); err != nil {
		t.Fatalf("publish production: %v", err)
	}
	check("", f.deployments["production"].ID)
	check("stage", "")
	if err := store.MarkDeploymentLive(ctx, f.deployments["stage"].ID); err != nil {
		t.Fatalf("publish stage: %v", err)
	}
	check("stage", f.deployments["stage"].ID)
	dark, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatalf("create dark stage: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, dark.ID); err != nil {
		t.Fatalf("publish dark stage: %v", err)
	}
	check("stage", f.deployments["stage"].ID)
	manifest := f.app.Manifest
	manifest.RevisionPinTTLSeconds = 1
	if _, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	if _, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "stage", 1,
		[]state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: dark.ID}}); err != nil {
		t.Fatalf("publish dark graph: %v", err)
	}
	// Active graphs can select a deliberately dark member; direct rows cannot.
	check("stage", dark.ID)
	newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatalf("create newer direct stage: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatalf("publish newer direct stage: %v", err)
	}
	check("stage", dark.ID)
	check("other", "")
	if _, err := state.ResolveEnvironmentDeployment(ctx, unavailableEnvironmentGraph{store}, f.app.ID, "stage"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unavailable graph fell back to direct deployment: %v", err)
	}
	if _, err := state.ResolveEnvironmentDeployment(ctx, store, f.app.ID, "invalid scope"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid environment selector: %v", err)
	}
}
