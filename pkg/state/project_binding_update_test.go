package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreUpdateProjectBindingIsAccountScopedAndConflictAtomic(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "projects@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "first", RepoFullName: "acme/first", ProductionBranch: "main", InstallID: 7})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "second", RepoFullName: "acme/second", ProductionBranch: "main", InstallID: 7})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.UpdateProjectBinding(ctx, "another-account", first.ID, "acme/new", "release", 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account update err = %v, want ErrNotFound", err)
	}
	if _, err := store.UpdateProjectBinding(ctx, acct.ID, first.ID, second.RepoFullName, "release", second.InstallID); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting update err = %v, want ErrConflict", err)
	}
	if got, err := store.ProjectByRepo(ctx, acct.ID, first.InstallID, first.RepoFullName); err != nil || got.ID != first.ID {
		t.Fatalf("old binding after conflict = %+v, err=%v", got, err)
	}

	updated, err := store.UpdateProjectBinding(ctx, acct.ID, first.ID, "", "release", 0)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RepoFullName != "" || updated.InstallID != 0 || updated.ProductionBranch != "release" {
		t.Fatalf("updated project = %+v", updated)
	}
	if _, err := store.ProjectByRepo(ctx, acct.ID, first.InstallID, first.RepoFullName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old repository index remained after unbind: %v", err)
	}
}
