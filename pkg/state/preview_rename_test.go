package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewRenameStore is the slice of the store the rename tests use.
type previewRenameStore interface {
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, app state.App) (state.App, error)
	RenameApp(ctx context.Context, accountID, oldSlug, newSlug string) (state.App, error)
	AppByID(ctx context.Context, id string) (state.App, error)
}

// seedPRPreview creates a production app and an open PR #7 preview of it.
func seedPRPreview(t *testing.T, store previewRenameStore) (state.Account, state.App, state.App) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	acct, err := store.CreateAccount(ctx, "preview-rename-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "web-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	preview, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pr-7-" + production.Slug, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: production.Slug,
		PreviewPrNumber: 7, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	return acct, production, preview
}

// Renaming a production app relinks its PR previews: left on the old slug,
// the preview janitor read them as orphaned and tore them down.
func exerciseRenameRelinksPRPreviews(t *testing.T, store previewRenameStore) {
	ctx := context.Background()
	acct, production, preview := seedPRPreview(t, store)
	renamed := production.Slug + "-x"
	if _, err := store.RenameApp(ctx, acct.ID, production.Slug, renamed); err != nil {
		t.Fatalf("RenameApp: %v", err)
	}
	got, err := store.AppByID(ctx, preview.ID)
	if err != nil || got.PreviewOfSlug != renamed || got.Slug != preview.Slug {
		t.Fatalf("preview after rename = (%+v, %v), want linked to %s on its old host", got, err, renamed)
	}
	if marker, ok := store.(state.OrphanedPRPreviewMarker); ok {
		if _, err := marker.MarkOrphanedPRPreviewsForTeardown(ctx, 1000); err != nil {
			t.Fatal(err)
		}
		if got, err := store.AppByID(ctx, preview.ID); err != nil || got.PreviewPrState != state.PreviewPrStateOpen {
			t.Fatalf("preview after orphan sweep = (%+v, %v), want open", got, err)
		}
	}
}

func TestMemStoreRenameRelinksPRPreviews(t *testing.T) {
	exerciseRenameRelinksPRPreviews(t, state.NewMemStore())
}

func TestPgStoreRenameRelinksPRPreviews(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseRenameRelinksPRPreviews(t, state.NewPgStore(pool))
}

// A GitHub repository rename carries the PR preview environments to the new
// name. Left behind, they never closed and kept their members from being
// retired when the PR's next head (delivered under the new name) replaced
// them.
func TestPgStoreRenameGitHubRepositoryMovesPRPreviewSets(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)
	acct, production, preview7 := seedPRPreview(t, store)
	expires := time.Now().Add(time.Hour)
	preview9, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pr-9-" + production.Slug, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: production.Slug,
		PreviewPrNumber: 9, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	previews := map[int]string{7: preview7.ID, 9: preview9.ID}
	installation := time.Now().UnixNano()%1_000_000_000 + 1
	oldName, newName := "octo/old-"+preview7.ID[:8], "octo/new-"+preview7.ID[:8]
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	put := func(repo string, pr int, sha string) {
		t.Helper()
		if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: installation, RepoFullName: repo, PRNumber: pr,
			CommitSHA: sha, RootAppID: previews[pr], MemberAppIDs: []string{previews[pr]}}); err != nil {
			t.Fatalf("put set %s#%d: %v", repo, pr, err)
		}
	}
	put(oldName, 7, shaA)
	put(oldName, 9, shaA)
	put(newName, 9, shaB) // already re-recorded under the new name

	if err := store.RenameGitHubRepository(ctx, installation, oldName, newName); err != nil {
		t.Fatalf("RenameGitHubRepository: %v", err)
	}
	moved, err := store.GetPRPreviewSet(ctx, installation, newName, 7)
	if err != nil || moved.CommitSHA != shaA || moved.Closed {
		t.Fatalf("moved set = (%+v, %v), want PR #7 open under the new name", moved, err)
	}
	kept, err := store.GetPRPreviewSet(ctx, installation, newName, 9)
	if err != nil || kept.CommitSHA != shaB {
		t.Fatalf("new-name set = (%+v, %v), want the newer head kept", kept, err)
	}
	for _, pr := range []int{7, 9} {
		if _, err := store.GetPRPreviewSet(ctx, installation, oldName, pr); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old-name set #%d = %v, want gone", pr, err)
		}
	}
}
