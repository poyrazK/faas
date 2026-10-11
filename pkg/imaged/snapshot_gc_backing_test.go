package imaged

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Snapshot GC removed mem, vmstate and drive but left the ADR-510 backing
// identity, orphaning one object per collected capture (H5-13).
func TestDeleteSnapshotsAndFilesRemovesBackingIdentity(t *testing.T) {
	store := state.NewMemStore()
	appID, depID, snapID := seedSnapshotWithApp(t, store, 1, 1)
	app, err := store.AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := state.Snapshot{DeploymentID: depID, StorageKey: state.SnapshotCaptureMemKey(depID, state.SnapshotTierInit, "gc-capture")}
	keys := []string{snap.StorageKey, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)}
	for _, key := range keys {
		if key == "" {
			t.Fatalf("capture key set incomplete: %v", keys)
		}
		if err := be.Put(context.Background(), key, strings.NewReader("capture")); err != nil {
			t.Fatal(err)
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	loop := &Loop{store: store, log: quiet, handler: &Handler{store: store, log: quiet, storage: be}}
	if err := loop.deleteSnapshotsAndFiles(context.Background(), []deleteTarget{{
		ID: snapID, DeploymentID: depID, AppSlug: app.Slug, Tier: state.SnapshotTierInit, StorageKey: snap.StorageKey,
	}}); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		rc, err := be.Get(context.Background(), key)
		if err == nil {
			_ = rc.Close()
			t.Errorf("%s survived snapshot GC", key)
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("get %s: %v", key, err)
		}
	}
}
