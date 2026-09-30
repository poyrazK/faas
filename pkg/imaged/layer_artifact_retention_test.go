package imaged

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ADR-375: deletion retries survive removal of snapshot rows and a daemon restart.
func TestLayerArtifactDeletionRetriesWithoutSnapshotRows(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "layers/orphan.ext4"
	if err := backend.Put(ctx, key, strings.NewReader("immutable layer")); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	failed := &Handler{store: store, log: log, storage: &failingDeleteStorage{StorageBackend: backend, err: storage.ErrDeleteUnsupported}}
	if err := failed.deleteLayerArtifact(ctx, failed.storage, key); !errors.Is(err, storage.ErrDeleteUnsupported) {
		t.Fatalf("backend failure: %v", err)
	}
	claims, err := store.PendingLayerArtifactDeletions(ctx)
	if err != nil || len(claims) != 1 || claims[0].DeletionID == "" {
		t.Fatalf("durable deletion claim: %+v, %v", claims, err)
	}
	if snapshots, err := store.ListSnapshotsForGC(ctx); err != nil || len(snapshots) != 0 {
		t.Fatalf("test requires no snapshot rows: %+v, %v", snapshots, err)
	}
	restarted := &Handler{store: store, log: log, storage: backend}
	loop := NewLoop(LoopConfig{Handler: restarted, Store: store, Log: log, Now: time.Now,
		LvUsedPct: func(context.Context) (float64, error) { return 0, nil }})
	loop.runGCTick(ctx, time.Now())
	if pending, err := store.PendingLayerArtifactDeletions(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("restarted GC did not finish deletion: %+v, %v", pending, err)
	}
	if rc, err := backend.Get(ctx, key); err == nil {
		_ = rc.Close()
		t.Fatal("claimed layer survived successful retry")
	} else if !storage.IsNotFound(err) {
		t.Fatal(err)
	}
	replayed, eligible, err := store.ClaimLayerArtifactDeletion(ctx, key)
	if err != nil || !eligible || replayed.DeletionID != claims[0].DeletionID || replayed.State != state.LayerArtifactDeleted {
		t.Fatalf("deletion identity changed on replay: %+v, %v", replayed, err)
	}
}

// ADR-375: a prepared stage keeps a shared immutable layer after source cleanup.
func TestCleanupRetainsLayerReusedByStage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	appsRoot, slug, appID, sourceID, backend := newAppDir(t, store)
	key := touchExt4(t, backend, slug, sourceID)
	if err := store.SetDeploymentRootfs(ctx, sourceID, "/source.ext4", key, 1024); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, sourceID); err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, stage.ID, "/source.ext4", key, 1024); err != nil {
		t.Fatal(err)
	}
	sidecarKey := "layers/shared-sidecar.ext4"
	if err := backend.Put(ctx, sidecarKey, strings.NewReader("sidecar layer")); err != nil {
		t.Fatal(err)
	}
	for _, deploymentID := range []string{sourceID, stage.ID} {
		if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: deploymentID, SidecarName: "metrics", StorageKey: sidecarKey, Bytes: 512}); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{store: store, appsRoot: appsRoot, storage: backend, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := h.cleanupDeploymentFiles(ctx, sourceID, true); err != nil {
		t.Fatal(err)
	}
	// A fresh daemon must retry the saved cleanup request even with no
	// snapshot rows left to select in the normal GC query.
	restarted := &Handler{store: store, appsRoot: appsRoot, storage: backend, log: h.log}
	loop := NewLoop(LoopConfig{Handler: restarted, Store: store, Log: h.log, Now: time.Now,
		LvUsedPct: func(context.Context) (float64, error) { return 0, nil }})
	loop.runGCTick(ctx, time.Now())
	rc, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatalf("source cleanup deleted stage layer: %v", err)
	}
	_ = rc.Close()
	if err := store.ClearDeployment(ctx, stage.ID, "test"); err != nil {
		t.Fatal(err)
	}
	loop.runGCTick(ctx, time.Now())
	for _, key := range []string{key, sidecarKey} {
		if rc, err := backend.Get(ctx, key); err == nil {
			_ = rc.Close()
			t.Fatalf("unreferenced layer was never reclaimed: %s", key)
		} else if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
}
