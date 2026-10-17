// adr: 1351 — exact candidate service allocation, expiry and routing preservation.
package state_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProjectReleaseStagingMem(t *testing.T) {
	projectReleaseStagingSuite(t, state.NewMemStore(), state.DefaultLocalNodeName)
}
func TestProjectReleaseStagingPG(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	projectReleaseStagingSuite(t, store, resolveDefaultLocal(t, ctx, store))
}
func projectReleaseStagingSuite(t *testing.T, store state.Store, node string) {
	t.Helper()
	ctx := t.Context()
	f := newDeactivationFixture(t, store)
	app := f.apps[0]
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Desired: 2, Max: 2}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	selected := map[string]string{}
	candidate := ""
	for _, member := range f.release.Members {
		for _, a := range f.apps {
			if a.ID == member.AppID {
				selected[a.Slug] = uuid.MustParse(member.DeploymentID).String()
				if a.ID == app.ID {
					candidate = member.DeploymentID
				}
			}
		}
	}
	prepared, problem := bindings.PrepareProjectRelease(ctx, store.(bindings.ProjectReleaseCatalog), f.account.ID, f.project, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: selected})
	if problem != nil {
		t.Fatal(problem)
	}
	staging := store.(state.ProjectReleaseStagingStore)
	request := api.StageProjectReleaseRequest{Manifest: prepared, TTLSeconds: 1800}
	stage, err := staging.StageProjectRelease(ctx, f.account.ID, f.project.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Status != "waiting" || len(stage.Members) != 1 || stage.Members[0].DeploymentID != selected[app.Slug] || stage.Members[0].Desired != 2 || stage.Members[0].Ready != 0 {
		t.Fatalf("bad initial stage: %+v", stage)
	}
	again, err := staging.StageProjectRelease(ctx, f.account.ID, f.project.ID, request)
	if err != nil || again.ID != stage.ID || !again.ExpiresAt.Equal(stage.ExpiresAt) {
		t.Fatalf("stage retry extended or duplicated lease: %+v %v", again, err)
	}
	other := request
	other.Manifest.TTLSeconds = 1799
	if _, err := staging.StageProjectRelease(ctx, f.account.ID, f.project.ID, other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping manifest: %v", err)
	}
	if _, err := staging.ProjectReleaseStage(ctx, uuid.NewString(), f.project.ID, "production", stage.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account stage exposed: %v", err)
	}
	for replicas := 1; replicas <= 2; replicas++ {
		if _, err := store.CreateInstanceWithMode(ctx, app.ID, candidate, "running", 128, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
		view, err := staging.ProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Members[0].Ready != replicas || (view.Status == "ready") != (replicas == 2) {
			t.Fatalf("readiness ignored desired count: %+v", view)
		}
	}
	active, err := store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
	if err != nil || active.ID != f.release.ID {
		t.Fatalf("staging changed graph: %+v %v", active, err)
	}
	for _, member := range f.fallback {
		dep, err := store.DeploymentByID(ctx, member.DeploymentID)
		if err != nil || dep.TrafficPercent != 100 {
			t.Fatalf("staging changed fallback: %+v %v", dep, err)
		}
	}
	view, err := staging.CancelProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID)
	if err != nil || view.Status != "cancelled" {
		t.Fatalf("cancel failed: %+v %v", view, err)
	}
	again, err = staging.CancelProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID)
	if err != nil || again.Status != "cancelled" {
		t.Fatalf("cancel retry failed: %+v %v", again, err)
	}
	newStage, err := staging.StageProjectRelease(ctx, f.account.ID, f.project.ID, request)
	if err != nil || newStage.ID == stage.ID {
		t.Fatalf("cancelled lease reused: %+v %v", newStage, err)
	}
	revision := prepared.ExpectedPolicyRevisions[app.Slug]
	if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, f.account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	view, err = staging.ProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", newStage.ID)
	if err != nil || view.Status != "blocked" || view.Reason != "manifest_changed" {
		t.Fatalf("stale policy reported ready: %+v %v", view, err)
	}
	if _, err := staging.StageProjectRelease(ctx, f.account.ID, f.project.ID, request); !errors.Is(err, state.ErrBindingReleasePolicyRevision) {
		t.Fatalf("stale policy restaged: %v", err)
	}
}
