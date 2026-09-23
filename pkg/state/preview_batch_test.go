package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStorePRPreviewBatchAtomicQuotaAndRetry(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "preview-batch@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"api", "worker", "db"} {
		if _, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: slug, Status: AppActive}); err != nil {
			t.Fatal(err)
		}
	}
	limits := api.Limits{DeployedApps: 5}
	previews := []App{
		{AccountID: account.ID, ProjectID: "project", Slug: "pr-42-api", PreviewOfSlug: "api", PreviewPrNumber: 42, Status: AppActive},
		{AccountID: account.ID, ProjectID: "project", Slug: "pr-42-db", PreviewOfSlug: "db", PreviewPrNumber: 42, Status: AppActive},
		{AccountID: account.ID, ProjectID: "project", Slug: "pr-42-worker", PreviewOfSlug: "worker", PreviewPrNumber: 42, Status: AppActive},
	}
	if _, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, limits); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-quota batch = %v, want quota error", err)
	}
	for _, app := range previews {
		if _, err := store.AppBySlug(ctx, app.Slug); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial preview %q after rollback: %v", app.Slug, err)
		}
	}
	root, err := store.CreateAppIfUnderQuota(ctx, previews[0], limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, limits); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("retry with one preserved preview = %v, want quota error", err)
	}
	kept, err := store.AppBySlug(ctx, root.Slug)
	if err != nil || kept.ID != root.ID {
		t.Fatalf("preexisting preview was not preserved: (%+v, %v)", kept, err)
	}
	for _, app := range previews[1:] {
		if _, err := store.AppBySlug(ctx, app.Slug); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial sibling %q after retry: %v", app.Slug, err)
		}
	}
	// Increasing the cap makes the same batch succeed without replacing root.
	created, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, api.Limits{DeployedApps: 6})
	if err != nil || len(created) != len(previews) || created[0].ID != root.ID {
		t.Fatalf("idempotent batch = (%+v, %v)", created, err)
	}
	if _, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, api.Limits{DeployedApps: 6}); err != nil {
		t.Fatalf("full-capacity retry: %v", err)
	}
}
