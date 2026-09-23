//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPRPreviewBatchRollsBackOnQuotaAndReusesExisting(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "preview-batch-atomic@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"batch-api", "batch-worker", "batch-db"} {
		if _, err := store.CreateAppIfUnderQuota(ctx, state.App{
			AccountID: account.ID, Slug: slug, Status: state.AppActive,
		}, api.Limits{DeployedApps: 5}); err != nil {
			t.Fatal(err)
		}
	}
	previews := []state.App{
		{AccountID: account.ID, Slug: "pr-42-batch-api", PreviewOfSlug: "batch-api", PreviewPrNumber: 42, PreviewPrState: state.PreviewPrStateOpen, Status: state.AppActive},
		{AccountID: account.ID, Slug: "pr-42-batch-db", PreviewOfSlug: "batch-db", PreviewPrNumber: 42, PreviewPrState: state.PreviewPrStateOpen, Status: state.AppActive},
		{AccountID: account.ID, Slug: "pr-42-batch-worker", PreviewOfSlug: "batch-worker", PreviewPrNumber: 42, PreviewPrState: state.PreviewPrStateOpen, Status: state.AppActive},
	}
	limits := api.Limits{DeployedApps: 5}
	if _, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, limits); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("over-quota batch = %v, want quota error", err)
	}
	for _, app := range previews {
		if _, err := store.AppBySlug(ctx, app.Slug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("partial preview %q after rollback: %v", app.Slug, err)
		}
	}
	root, err := store.CreateAppIfUnderQuota(ctx, previews[0], limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, limits); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("retry with existing root = %v, want quota error", err)
	}
	kept, err := store.AppBySlug(ctx, root.Slug)
	if err != nil || kept.ID != root.ID {
		t.Fatalf("preexisting root was not preserved: (%+v, %v)", kept, err)
	}
	for _, app := range previews[1:] {
		if _, err := store.AppBySlug(ctx, app.Slug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("partial sibling %q after retry: %v", app.Slug, err)
		}
	}
	created, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, previews, api.Limits{DeployedApps: 6})
	if err != nil || len(created) != len(previews) || created[0].ID != root.ID {
		t.Fatalf("idempotent batch = (%+v, %v)", created, err)
	}
}
