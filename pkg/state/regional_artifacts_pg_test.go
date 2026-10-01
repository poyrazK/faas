//go:build !no_pg

package state_test

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRegionalArtifactInventoryPreservesCommittedReferences(t *testing.T) {
	store, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, store, ctx, "regional-inventory-", "regional-inventory")
	rootfs := region.ArtifactKey("eu-fsn", "apps/committed/custom.ext4")
	if err := store.SetDeploymentRootfs(ctx, depID, "/diagnostic.ext4", rootfs, 4096); err != nil {
		t.Fatal(err)
	}
	key := region.ArtifactKey("eu-fsn", state.SnapshotCaptureMemKey(depID, state.SnapshotTierWarm, "00000000-0000-0000-0000-000000002429"))
	snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: depID, StorageKey: key, Tier: state.SnapshotTierWarm, FCVersion: "1.10.0"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListSnapshotsForGC(ctx)
	if err != nil || len(rows) != 1 || rows[0].RootfsKey != rootfs || rows[0].StorageKey != key {
		t.Fatalf("active GC=%+v err=%v", rows, err)
	}
	artifacts, err := store.ListAppDeletionArtifacts(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, artifact := range artifacts {
		keys = append(keys, artifact.Key)
	}
	for _, want := range []string{rootfs, key, state.SnapshotVMStateKey(snapshot), state.SnapshotDriveKey(snapshot)} {
		if !slices.Contains(keys, want) {
			t.Fatalf("deletion inventory omitted %s: %v", want, keys)
		}
	}
	if _, err := store.MarkOldSnapshotsStale(ctx, []string{snapshot.ID}); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListSnapshotsPendingDelete(ctx)
	if err != nil || len(rows) != 1 || rows[0].RootfsKey != rootfs {
		t.Fatalf("pending GC=%+v err=%v", rows, err)
	}
	rows, err = store.ListSnapshotsStaleOlderThan(ctx, -time.Second)
	if err != nil || len(rows) != 1 || rows[0].RootfsKey != rootfs {
		t.Fatalf("stale GC=%+v err=%v", rows, err)
	}
}
