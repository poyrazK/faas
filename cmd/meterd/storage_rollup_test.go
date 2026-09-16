package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// The production adapter must supply the retained-layer source to the meter
// rollup. This exercises the complete in-process path from deployment artifact
// metadata through the daily customer storage row.
func TestStorageRollupUsesRetainedDeploymentLayers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "storage-rollup@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "storage-rollup"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: "image", ImageDigest: "sha256:retained", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	const rootfsBytes int64 = 25_973_545
	const sidecarBytes int64 = 2_097_152
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/var/lib/faas/apps/storage-rollup/rootfs.ext4", "apps/storage-rollup/rootfs.ext4", rootfsBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: deployment.ID, SidecarName: "proxy", StorageKey: "apps/storage-rollup/proxy.ext4",
		Bytes: sidecarBytes, ContentDigest: "sha256:sidecar",
	}); err != nil {
		t.Fatal(err)
	}

	adapter := storageStoreAdapter{s: store}
	day := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	written, err := meter.StorageRollupOnce(ctx, adapter, day, adapter.RetainedLayerBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}
	rows, err := store.StorageUsage(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LayerBytes != rootfsBytes+sidecarBytes {
		t.Fatalf("storage rows = %+v, want layer_bytes=%d", rows, rootfsBytes+sidecarBytes)
	}
}
