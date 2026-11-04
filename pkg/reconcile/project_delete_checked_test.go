package reconcile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProjectDeletionRechecksStagesAndClones(t *testing.T) {
	for _, competing := range []string{"stage", "clone", "none"} {
		t.Run(competing, func(t *testing.T) { testProjectDeletionRechecksStagesAndClones(t, state.NewMemStore(), competing) })
	}
}

func testProjectDeletionRechecksStagesAndClones(t *testing.T, store state.Store, competing string) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "delete-checked@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "delete-checked"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "delete-checked-web", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	// The handler's earlier inventory cannot authorize a later unchecked delete.
	envs, err := store.ListProjectEnvironments(ctx, acct.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range envs {
		if !state.IsProductionScope(env.Slug) {
			t.Fatal("fixture already has a stage")
		}
	}
	if competing == "stage" {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
	}
	if competing == "clone" {
		_, err := store.(state.ProjectEnvironmentCloneOperationStore).CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
			AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "staging",
			IdempotencyKey: "delete-checked", SourceRevisionHash: strings.Repeat("a", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err = store.(state.CheckedProjectDeleteStore).DeleteProjectIfNoStages(ctx, acct.ID, project.ID)
	if competing == "none" {
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ProjectBySlug(ctx, acct.ID, project.Slug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("project survived: %v", err)
		}
		return
	}
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("competing %s did not block delete: %v", competing, err)
	}
	if _, err := store.ProjectBySlug(ctx, acct.ID, project.Slug); err != nil {
		t.Fatal(err)
	}
	got, err := store.AppByID(ctx, app.ID)
	if err != nil || got.ProjectID != project.ID {
		t.Fatalf("guard detached app: %+v %v", got, err)
	}
	if competing == "stage" {
		if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging"); err != nil {
			t.Fatalf("guard deleted stage: %v", err)
		}
	}
}
