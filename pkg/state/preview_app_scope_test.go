package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStorePreviewAppByProjectWorkload(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "preview-scope@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "preview-scope"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	create := func(slug string, prNumber int, status AppStatus) App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, App{
			AccountID: account.ID, ProjectID: project.ID, Slug: slug,
			WorkloadName: "billing", PreviewOfSlug: "billing", PreviewPrNumber: prNumber,
			RAMMB: 128, Status: status,
		})
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", slug, createErr)
		}
		return app
	}
	want := create("pr-42-billing", 42, AppActive)
	_ = create("pr-43-billing", 43, AppActive)
	_ = create("pr-44-billing", 44, AppDeleted)

	got, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 42, "billing")
	if err != nil {
		t.Fatalf("PreviewAppByProjectWorkload: %v", err)
	}
	if got.ID != want.ID {
		t.Fatalf("resolved app = %q, want %q", got.ID, want.ID)
	}
	for _, lookup := range []struct {
		account, project string
		pr               int
		workload         string
	}{
		{account.ID, project.ID, 43, "orders"},
		{account.ID, project.ID, 44, "billing"},
		{account.ID, project.ID, 0, "billing"},
		{"other-account", project.ID, 42, "billing"},
		{account.ID, "other-project", 42, "billing"},
	} {
		if _, lookupErr := store.PreviewAppByProjectWorkload(ctx, lookup.account, lookup.project, lookup.pr, lookup.workload); !errors.Is(lookupErr, ErrNotFound) {
			t.Errorf("lookup %+v error = %v, want ErrNotFound", lookup, lookupErr)
		}
	}
}
