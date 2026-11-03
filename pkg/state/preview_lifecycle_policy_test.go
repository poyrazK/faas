package state_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewLifecycleStore is the slice of the store these tests use.
type previewLifecycleStore interface {
	previewRenameStore
	SoftDeleteAppCascade(ctx context.Context, id string) (state.App, error)
	SetPreviewPrState(ctx context.Context, appID, prState string) (state.App, error)
	RestoreApp(ctx context.Context, id string, limits api.Limits) (state.App, error)
	ListAccountDeletedApps(ctx context.Context, accountID string, beforeAt time.Time, beforeID string, limit int) ([]state.DeletedAppRecord, error)
	DeleteGithubInstallBinding(ctx context.Context, appID string) error
	PutPRPreviewSet(ctx context.Context, set state.PRPreviewSet) error
}

// A torn-down preview keeps a soft-deleted row for the restore window. A
// restore brought it back active but still torn_down: the janitor skips
// those and a preview that is not open holds no allowance, so it served its
// PR code forever outside the quota.
func exerciseTornDownPreviewNotRestorable(t *testing.T, store previewLifecycleStore) {
	ctx := context.Background()
	acct, _, preview := seedPRPreview(t, store)
	if _, err := store.SoftDeleteAppCascade(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPreviewPrState(ctx, preview.ID, state.PreviewPrStateTornDown); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreApp(ctx, preview.ID, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("RestoreApp(torn-down preview) = %v, want ErrConflict", err)
	}
	if got, err := store.AppByID(ctx, preview.ID); err != nil || got.Status != state.AppDeleted {
		t.Fatalf("preview after refused restore = (%+v, %v), want still deleted", got, err)
	}
	deleted, err := store.ListAccountDeletedApps(ctx, acct.ID, time.Time{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range deleted {
		if row.ID == preview.ID {
			t.Fatalf("deleted apps list offers the torn-down preview %s for restore", preview.Slug)
		}
	}
}

func TestMemStoreTornDownPreviewNotRestorable(t *testing.T) {
	exerciseTornDownPreviewNotRestorable(t, state.NewMemStore())
}

func TestPgStoreTornDownPreviewNotRestorable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseTornDownPreviewNotRestorable(t, state.NewPgStore(pool))
}

// Disconnecting a repository marks the app's PR environments stale, as an
// installation revoke does. An environment belongs to its root's production
// app: another app's environment that previews this one as a dependency is
// left alone.
func exerciseUnbindStalesPRPreviews(t *testing.T, store previewLifecycleStore) {
	ctx := context.Background()
	acct, production, preview := seedPRPreview(t, store)
	other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: production.Slug + "-b", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	createPreview := func(parent string, pr int) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pr-" + strconv.Itoa(pr) + "-" + parent,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: parent,
			PreviewPrNumber: pr, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	otherRoot := createPreview(other.Slug, 8)
	dependency := createPreview(production.Slug, 8) // the unbound app as a dependency of other's PR #8
	if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 42, RepoFullName: "octo/" + other.Slug, PRNumber: 8,
		CommitSHA: strings.Repeat("a", 40), RootAppID: otherRoot.ID, MemberAppIDs: []string{dependency.ID, otherRoot.ID}}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteGithubInstallBinding(ctx, production.ID); err != nil {
		t.Fatalf("DeleteGithubInstallBinding: %v", err)
	}
	for _, tc := range []struct {
		app  state.App
		want string
	}{
		{preview, state.PreviewPrStateStale},
		{otherRoot, state.PreviewPrStateOpen},
		{dependency, state.PreviewPrStateOpen},
	} {
		got, err := store.AppByID(ctx, tc.app.ID)
		if err != nil || got.PreviewPrState != tc.want {
			t.Fatalf("%s after unbind = (%q, %v), want %q", tc.app.Slug, got.PreviewPrState, err, tc.want)
		}
	}
}

func TestMemStoreUnbindStalesPRPreviews(t *testing.T) {
	exerciseUnbindStalesPRPreviews(t, state.NewMemStore())
}

func TestPgStoreUnbindStalesPRPreviews(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseUnbindStalesPRPreviews(t, state.NewPgStore(pool))
}
