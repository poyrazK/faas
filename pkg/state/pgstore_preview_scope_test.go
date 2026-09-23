//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStorePreviewAppByProjectWorkload(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "preview-scope-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "preview-scope-pg"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	create := func(slug string, prNumber int) state.App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, state.App{
			AccountID: account.ID, ProjectID: project.ID, Slug: slug,
			WorkloadName: "billing", PreviewOfSlug: "billing", PreviewPrNumber: prNumber,
			RAMMB: 128, Status: state.AppActive,
		})
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", slug, createErr)
		}
		return app
	}
	want := create("pr-42-preview-scope-pg-billing", 42)
	otherPR := create("pr-43-preview-scope-pg-billing", 43)
	if _, err := store.SoftDeleteAppCascade(ctx, otherPR.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}

	got, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 42, "billing")
	if err != nil {
		t.Fatalf("PreviewAppByProjectWorkload: %v", err)
	}
	if got.ID != want.ID {
		t.Fatalf("resolved app = %q, want %q", got.ID, want.ID)
	}
	if _, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 43, "billing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted preview lookup error = %v, want ErrNotFound", err)
	}
	if _, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 42, "orders"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other workload lookup error = %v, want ErrNotFound", err)
	}
}
