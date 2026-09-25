//go:build !no_pg

// adr: 233
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentByIDForStableHost(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "stable-environment-host@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stable-host", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ProjectEnvironmentByID(ctx, environment.ID)
	if err != nil || got.ID != environment.ID || got.ProjectID != project.ID || got.AccountID != account.ID || got.Slug != "staging" {
		t.Fatalf("environment by id = %+v err=%v", got, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentByID(ctx, environment.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted environment lookup error = %v, want ErrNotFound", err)
	}
}

func TestPgProjectEnvironmentPreviewIdentityIsUniqueAndQueryable(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "preview-environment-identity@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "preview-identity"})
	if err != nil {
		t.Fatal(err)
	}
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	created, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "pr-381",
		PreviewPRNumber: 381, PreviewHeadSHA: sha,
	})
	if err != nil {
		t.Fatal(err)
	}
	byID, err := store.ProjectEnvironmentByID(ctx, created.ID)
	if err != nil || byID.PreviewPRNumber != 381 || byID.PreviewHeadSHA != sha {
		t.Fatalf("environment by id = %+v err=%v", byID, err)
	}
	byPR, err := store.ProjectEnvironmentByPreviewPR(ctx, account.ID, project.ID, 381)
	if err != nil || byPR.ID != created.ID {
		t.Fatalf("environment by PR = %+v err=%v", byPR, err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: account.ID, ProjectID: project.ID, Slug: "pr-381-other",
		PreviewPRNumber: 381, PreviewHeadSHA: sha,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate PR identity error = %v, want ErrConflict", err)
	}
}
