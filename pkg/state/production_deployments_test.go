// adr: 568
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProductionDeploymentSelection(t *testing.T) {
	testProductionDeploymentSelection(t, state.NewMemStore())
}

func testProductionDeploymentSelection(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "production-selection@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "production-selection"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID,
		Slug: "production-selection-api", RAMMB: 128, CPUMillicores: 250, Status: state.AppActive,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 600}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(scope string, dark bool) state.Deployment {
		t.Helper()
		status := state.DeployPending
		if dark {
			status = state.DeployLive
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope,
			Kind: state.DeploymentKindImage, ImageDigest: "sha256:selection", Status: status, TrafficPercentExplicit: dark})
		if err != nil {
			t.Fatalf("create %s deployment: %v", scope, err)
		}
		if !dark {
			if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
				t.Fatalf("publish %s deployment: %v", scope, err)
			}
		}
		return deployment
	}
	check := func(want string) {
		t.Helper()
		deployment, err := state.ResolveProductionDeployment(ctx, store, app.ID)
		if want == "" {
			if !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stage-only production selection = %+v, %v", deployment, err)
			}
		} else if err != nil || deployment.ID != want {
			t.Fatalf("production selection = %+v, %v; want %s", deployment, err, want)
		}
	}
	create("staging", false)
	check("")
	legacy := create("default", false)
	create("staging", false)
	check(legacy.ID)
	create("production", true)
	check(legacy.ID)
	production := create("production", false)
	create("pr-42", false)
	check(production.ID)
	releases := store.(state.ProjectReleaseSetStore)
	if _, err := releases.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 600,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: production.ID}}); err != nil {
		t.Fatalf("publish production graph: %v", err)
	}
	// A newer direct deployment cannot silently replace the active graph.
	create("production", false)
	create("staging", false)
	check(production.ID)
}
