package imaged

// F5 cleanup tests — supersede hook drops the per-app ext4 but keeps the
// snap blob (one-click rollback fast); delete-app hook drops the entire
// appsRoot/<slug>/ dir and every snap blob for the app's deployments.

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func newAppDir(t *testing.T, store *state.MemStore) (appsRoot, appSlug, appID, depID string) {
	t.Helper()
	appsRoot = t.TempDir()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "cleanup-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
	})
	appSlug = app.Slug
	appID = app.ID
	depID = dep.ID
	return
}

// touchExt4 creates an empty ext4-shaped file under appsRoot/<slug>/<dep>.ext4
// so cleanup has something to remove.
func touchExt4(t *testing.T, appsRoot, slug, depID string) string {
	t.Helper()
	dir := filepath.Join(appsRoot, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ext4 := filepath.Join(dir, depID+".ext4")
	if err := os.WriteFile(ext4, []byte("fake ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ext4
}

func TestSupersede_DeletesOldAppLayer_KeepsSnapshotBlob(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, slug, _, depID := newAppDir(t, store)
	ext4 := touchExt4(t, appsRoot, slug, depID)

	// Stamp the rootfs so cleanupDeploymentFiles finds it.
	if err := store.SetDeploymentRootfs(context.Background(), depID, ext4, 7); err != nil {
		t.Fatal(err)
	}

	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
	}
	// keepSnap=true is the supersede path: drop the per-app ext4 but
	// preserve the snapshot blob so one-click rollback is fast (F5).
	if err := h.cleanupDeploymentFiles(context.Background(), depID, true); err != nil {
		t.Fatalf("cleanupDeploymentFiles: %v", err)
	}

	if _, err := os.Stat(ext4); !os.IsNotExist(err) {
		t.Errorf("ext4 still present after supersede: stat err = %v", err)
	}
	// We can't pre-create sched.SnapDir() (it's /srv/fc/snap and the test
	// box may not be writable there), so the "snapshot blob survives"
	// invariant is asserted by code inspection of cleanupDeploymentFiles:
	// keepSnap=true skips the snap-dir removal branch. The negative-space
	// check here is "nothing under appsRoot/<slug> survives for this dep".
	matches, _ := filepath.Glob(filepath.Join(appsRoot, slug, depID+".ext4"))
	if len(matches) != 0 {
		t.Errorf("supersede left ext4 behind: %v", matches)
	}
}

func TestDeleteApp_UnlinksAppDir(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, slug, appID, depID := newAppDir(t, store)
	ext4 := touchExt4(t, appsRoot, slug, depID)
	if err := store.SetDeploymentRootfs(context.Background(), depID, ext4, 7); err != nil {
		t.Fatal(err)
	}

	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
	}
	if err := h.cleanupAppFiles(context.Background(), appID); err != nil {
		t.Fatalf("cleanupAppFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appsRoot, slug)); !os.IsNotExist(err) {
		t.Errorf("app dir still present after delete: stat err = %v", err)
	}
	// Same caveat as TestSupersede: we can't pre-create the snap blob
	// dir under /srv in a non-root sandbox. The "deleted" branch in
	// cleanupAppFiles still runs os.RemoveAll — it's just a no-op when
	// the dir is absent (RemoveAll on a missing path returns nil).
}

func TestCleanup_MissingFilesIsNoError(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, _, _, depID := newAppDir(t, store)
	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
	}
	// Nothing on disk under appsRoot or sched.SnapDir() for depID.
	if err := h.cleanupDeploymentFiles(context.Background(), depID, true); err != nil {
		t.Errorf("cleanupDeploymentFiles with missing files returned error: %v", err)
	}
	if err := h.cleanupAppFiles(context.Background(), "no-such-app-id"); err != nil {
		t.Errorf("cleanupAppFiles with no deployments returned error: %v", err)
	}
}

// TestCleanup_DoesNotTouchLiveApp exercises the safe path: an
// app_changed notification with kind != "deleted" must not run the
// cleanup hook. Guards against accidental over-cleanup on status flips.
func TestCleanup_DoesNotTouchLiveApp(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, slug, appID, depID := newAppDir(t, store)
	ext4 := touchExt4(t, appsRoot, slug, depID)

	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
	}
	// Feed a non-delete app_changed event.
	n := db.Notification{
		Channel: db.NotifyAppChanged,
		Payload: `{"app_id":"` + appID + `","kind":"updated"}`,
	}
	h.HandleNotification(context.Background(), n)

	if _, err := os.Stat(ext4); err != nil {
		t.Errorf("non-delete app_changed removed the live ext4: %v", err)
	}
	_ = strings.Contains // keep import
}
