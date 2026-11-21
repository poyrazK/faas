// adr: 685
package imaged

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// earlyStopFixture is a project where web declares a healthy dependency on
// backend, with candidate (web) pinned to dependency (backend), both still
// preparing.
func earlyStopFixture(t *testing.T) (*state.MemStore, *Handler, state.Deployment, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "early-stop@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "early-stop"})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(name string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	backend, web := createApp("backend"), createApp("web")
	manifest := web.Manifest
	manifest.ServiceBindings = []api.AppServiceBinding{{Service: "backend", Binding: api.ServiceBindingEnvKey("backend")}}
	manifest.ProjectDependencyConditions = map[string]string{"backend": api.ComposeDependencyHealthy}
	if _, err := store.UpdateApp(ctx, web.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	createDep := func(app state.App) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "docker.io/library/nginx:1.27"})
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	dependency, candidate := createDep(backend), createDep(web)
	h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
	return store, h, dependency, candidate
}

// GREG-01/04 follow-up: rv-ui built, scanned and prepared a snapshot for
// 6.5 minutes after the rv-cart deployment it was pinned to had failed.
func TestTransitionStopsCandidateWhosePinnedDependencyFailed(t *testing.T) {
	ctx := t.Context()
	store, h, dependency, candidate := earlyStopFixture(t)
	if _, err := store.SetDeploymentFailed(ctx, dependency.ID, "app_startup_timeout", "cold_boot_timeout"); err != nil {
		t.Fatal(err)
	}

	err := h.transitionWithStage(ctx, candidate.ID, state.StageSourceDownload, state.StageImageBuild, state.DeployImaging, "")
	if !errors.Is(err, errDependencyFailedEarly) {
		t.Fatalf("transition err = %v, want errDependencyFailedEarly", err)
	}
	got, err := store.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployFailed || got.ErrorCode != state.CodeDependencyFailed {
		t.Fatalf("candidate = status %s code %q error %q; want failed with %s", got.Status, got.ErrorCode, got.Error, state.CodeDependencyFailed)
	}
	var stages state.StageState
	_ = json.Unmarshal(got.StageState, &stages)
	if stages.Current == state.StageImageBuild {
		t.Fatalf("candidate entered image_build after its dependency failed: %s", got.StageState)
	}
}

func TestTransitionContinuesWhileDependencyIsStillPreparing(t *testing.T) {
	ctx := t.Context()
	store, h, _, candidate := earlyStopFixture(t)

	if err := h.transitionWithStage(ctx, candidate.ID, state.StageSourceDownload, state.StageImageBuild, state.DeployImaging, ""); err != nil {
		t.Fatalf("transition with a pending dependency: %v", err)
	}
	got, err := store.DeploymentByID(ctx, candidate.ID)
	if err != nil || got.Status != state.DeployImaging {
		t.Fatalf("candidate = %+v, %v; want imaging", got, err)
	}
	var stages state.StageState
	_ = json.Unmarshal(got.StageState, &stages)
	if stages.DependencyGate != nil && stages.DependencyGate.StartedAt != nil {
		t.Fatalf("early check started the activation gate's deadline: %s", got.StageState)
	}
}

// The early stop must complete the outbox item, not retry it; other
// failures still retry.
func TestEarlyDependencyStopSettlesTheNotification(t *testing.T) {
	// Wrapped the way HandleNotification's arms wrap pipeline errors.
	if err := settleEarlyDependencyStop(fmt.Errorf("handle deployment x: %w", errDependencyFailedEarly)); err != nil {
		t.Fatalf("early stop not settled: %v", err)
	}
	other := fmt.Errorf("handle deployment x: %w", db.ErrNotificationNotOwned)
	if err := settleEarlyDependencyStop(other); !errors.Is(err, db.ErrNotificationNotOwned) {
		t.Fatalf("other error swallowed: %v", err)
	}
	if err := settleEarlyDependencyStop(nil); err != nil {
		t.Fatal(err)
	}
}
