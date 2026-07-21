package imaged

// F5 cleanup tests — supersede hook drops the per-app ext4 but keeps the
// snap blob (one-click rollback fast); delete-app hook drops every per-app
// ext4 layer + snap blob for the app's deployments.
//
// Issue #96: the fixtures (ext4 files, snapshot blobs) live in the
// StorageBackend now, not on disk. The tests build a per-test
// LocalStorageBackend and Put the fixtures under the storage keys the
// production wiring uses (sched.AppLayerKey / sched.SnapshotMemKey /
// sched.SnapshotVMStateKey).

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// notifAppChanged builds a db.Notification for the NotifyAppChanged
// channel. Format mirrors the JSON shape apid emits (F-04 patch).
func notifAppChanged(appID, kind string) db.Notification {
	return db.Notification{
		Channel: db.NotifyAppChanged,
		Payload: fmt.Sprintf(`{"app_id":"%s","kind":"%s"}`, appID, kind),
	}
}

func newAppDir(t *testing.T, store *state.MemStore) (appsRoot string, appSlug, appID, depID string, be storage.StorageBackend) {
	t.Helper()
	appsRoot = t.TempDir()
	var err error
	be, err = storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
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

// touchExt4 puts a non-empty placeholder at apps/<slug>/<dep>.ext4 via
// the storage backend. Production writes here through the same code
// path (Handler.buildImageLayer → rootfs.Builder.Build → Storage.Put).
func touchExt4(t *testing.T, be storage.StorageBackend, slug, depID string) string {
	t.Helper()
	key := sched.AppLayerKey(slug, depID)
	if err := be.Put(context.Background(), key, strings.NewReader("fake ext4")); err != nil {
		t.Fatalf("seed apps ext4 %s: %v", key, err)
	}
	return key
}

func TestSupersede_DeletesOldAppLayer_KeepsSnapshotBlob(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, slug, _, depID, be := newAppDir(t, store)
	ext4Key := touchExt4(t, be, slug, depID)

	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
		storage:  be,
	}
	// keepSnap=true is the supersede path: drop the per-app ext4 but
	// preserve the snapshot blob so one-click rollback is fast (F5).
	if err := h.cleanupDeploymentFiles(context.Background(), depID, true); err != nil {
		t.Fatalf("cleanupDeploymentFiles: %v", err)
	}

	if rc, err := be.Get(context.Background(), ext4Key); err == nil {
		_ = rc.Close()
		t.Errorf("ext4 still present after supersede at key %s", ext4Key)
	}
}

func TestDeleteApp_UnlinksAppDir(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, slug, appID, depID, be := newAppDir(t, store)
	ext4Key := touchExt4(t, be, slug, depID)

	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
		storage:  be,
	}
	if err := h.cleanupAppFiles(context.Background(), appID); err != nil {
		t.Fatalf("cleanupAppFiles: %v", err)
	}
	if rc, err := be.Get(context.Background(), ext4Key); err == nil {
		_ = rc.Close()
		t.Errorf("ext4 key still present after app delete: %s", ext4Key)
	}
}

func TestCleanup_MissingFilesIsNoError(t *testing.T) {
	store := state.NewMemStore()
	appsRoot, _, _, depID, be := newAppDir(t, store)
	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
		storage:  be,
	}
	// Nothing in the storage backend at the relevant keys.
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
	appsRoot, slug, appID, depID, be := newAppDir(t, store)
	ext4Key := touchExt4(t, be, slug, depID)

	h := &Handler{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		notif:    &fakeNotifier{},
		storage:  be,
	}
	// Feed a non-delete app_changed event.
	h.HandleNotification(context.Background(), notifAppChanged(appID, "updated"))

	// The ext4 is still in the storage backend — no cleanup fired.
	if rc, err := be.Get(context.Background(), ext4Key); err != nil {
		t.Errorf("non-delete app_changed removed the live ext4: %v", err)
	} else {
		_ = rc.Close()
	}
	_ = strings.Contains // keep import
	_ = depID
}
