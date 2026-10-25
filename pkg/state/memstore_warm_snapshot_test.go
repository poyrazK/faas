package state_test

// Snapshot-tier MemStore tests (issue #470 / ADR-055). White-box
// package (`package state`) so the tests can call the unexported
// helpers (newMemStoreForTest, the seed IDs) directly — same pattern
// as memstore_trusted_signers_test.go. The pgstore equivalents live
// in pgstore_warm_snapshot_test.go (//go:build !no_pg + pgtest.Open).

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// seedTierAppAndDeployment inserts the minimum app + deployment rows
// that the Snapshot methods need. The IDs are fixed so a failure
// message can name them directly; tests don't depend on them.
func seedTierAppAndDeployment(t *testing.T, m *state.MemStore, deploymentID string) {
	t.Helper()
	ctx := context.Background()
	// MemStore's bare CreateApp doesn't require an account row to
	// exist (production code goes through CreateAppIfUnderQuota which
	// does, but we're calling the bare method here for unit-test
	// ergonomics).
	if _, err := m.CreateApp(ctx, state.App{
		ID: "00000000-0000-0000-0000-0000000000b0", AccountID: "00000000-0000-0000-0000-0000000000a0",
		Slug: "tier-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
		Status: state.AppActive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := m.CreateDeployment(ctx, state.Deployment{
		ID: deploymentID, AppID: "00000000-0000-0000-0000-0000000000b0",
		ImageDigest: "sha256:warm", Status: state.DeployLive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
}

// TestMemStore_CreateSnapshot_DefaultsTierToInit confirms the legacy
// contract: a snapshot created without a Tier ends up as "init" so
// pre-PR callers stay valid.
func TestMemStore_CreateSnapshot_DefaultsTierToInit(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c0")

	created, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c0",
		FCVersion:    "fc-1.0",
		MemBytes:     1024,
		DiskBytes:    512,
		StorageKey:   "snap/00000000-0000-0000-0000-0000000000c0/mem",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if created.Tier != state.SnapshotTierInit {
		t.Errorf("default Tier = %q, want %q", created.Tier, state.SnapshotTierInit)
	}
}

// TestMemStore_CreateSnapshot_TwoTierCoexist confirms the
// (deployment_id, tier) index behaviour: init + warm can both exist
// for one deployment; a second init insert on the same deployment is
// ErrConflict (R6 — the legacy branch becomes reachable again).
func TestMemStore_CreateSnapshot_TwoTierCoexist(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c1")

	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c1", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c1/mem", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c1", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c1/warm/mem", Tier: state.SnapshotTierWarm,
	}); err != nil {
		t.Fatalf("seed warm: %v", err)
	}
	// Duplicate init insert should hit ErrConflict (the same constraint
	// migration 00110's UNIQUE INDEX enforces on PgStore).
	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c1", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c1/init-dup/mem", Tier: state.SnapshotTierInit,
	}); !errors.Is(err, state.ErrConflict) {
		t.Errorf("dup init insert err = %v, want ErrConflict", err)
	}
}

// TestMemStore_LatestSnapshotForTier confirms the per-tier lookup.
// Init-only deployments read the init row; warm-only deployments
// read the warm row; a mixed deployment returns the right tier when
// explicitly asked.
func TestMemStore_LatestSnapshotForTier(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c2")

	initSnap := state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c2", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c2/mem", Tier: state.SnapshotTierInit,
		CreatedAt: time.Now().Add(-time.Hour),
	}
	warmSnap := state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c2", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c2/warm/mem", Tier: state.SnapshotTierWarm,
		CreatedAt: time.Now(),
	}
	if _, err := m.CreateSnapshot(ctx, initSnap); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, warmSnap); err != nil {
		t.Fatalf("seed warm: %v", err)
	}

	gotInit, err := m.LatestSnapshotForTier(ctx, "00000000-0000-0000-0000-0000000000c2", state.SnapshotTierInit)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier(init): %v", err)
	}
	if gotInit.Tier != state.SnapshotTierInit {
		t.Errorf("init lookup Tier = %q, want %q", gotInit.Tier, state.SnapshotTierInit)
	}
	if gotInit.StorageKey != initSnap.StorageKey {
		t.Errorf("init lookup key = %q, want %q", gotInit.StorageKey, initSnap.StorageKey)
	}

	gotWarm, err := m.LatestSnapshotForTier(ctx, "00000000-0000-0000-0000-0000000000c2", state.SnapshotTierWarm)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier(warm): %v", err)
	}
	if gotWarm.Tier != state.SnapshotTierWarm {
		t.Errorf("warm lookup Tier = %q, want %q", gotWarm.Tier, state.SnapshotTierWarm)
	}
	if gotWarm.StorageKey != warmSnap.StorageKey {
		t.Errorf("warm lookup key = %q, want %q", gotWarm.StorageKey, warmSnap.StorageKey)
	}
}

// TestMemStore_LatestSnapshot_WarmBeatsInitOnTie confirms the
// order-by clause from PgStore.LatestSnapshot is mirrored in memory:
// when both tiers have equal created_at, the warm row wins.
func TestMemStore_LatestSnapshot_WarmBeatsInitOnTie(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c3")
	now := time.Now()

	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c3", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c3/mem", Tier: state.SnapshotTierInit,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c3", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c3/warm/mem", Tier: state.SnapshotTierWarm,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed warm: %v", err)
	}

	got, err := m.LatestSnapshot(ctx, "00000000-0000-0000-0000-0000000000c3")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if got.Tier != state.SnapshotTierWarm {
		t.Errorf("LatestSnapshot Tier = %q, want warm (tie-break wins)", got.Tier)
	}
}

// TestMemStore_LatestSnapshot_IgnoresStale confirms stale-row
// filtering still works after the tier addition. A stale warm row is
// bypassed in favour of the init row, which keeps the
// warm-restore-failure → init-restore fallback path in schedd usable.
func TestMemStore_LatestSnapshot_IgnoresStale(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c4")

	warm, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c4", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c4/warm/mem", Tier: state.SnapshotTierWarm,
	})
	if err != nil {
		t.Fatalf("seed warm: %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c4", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c4/mem", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if err := m.MarkSnapshotStale(ctx, warm.ID); err != nil {
		t.Fatalf("MarkSnapshotStale: %v", err)
	}

	// LatestSnapshot must skip the stale warm row and return init.
	got, err := m.LatestSnapshot(ctx, "00000000-0000-0000-0000-0000000000c4")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if got.Tier != state.SnapshotTierInit {
		t.Errorf("LatestSnapshot Tier = %q, want init (warm was stale)", got.Tier)
	}
	// LatestSnapshotForTier(warm) must return ErrNotFound so schedd's
	// tier-fallback chain falls through to init.
	if _, err := m.LatestSnapshotForTier(ctx, "00000000-0000-0000-0000-0000000000c4", state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("LatestSnapshotForTier(warm) = %v, want ErrNotFound", err)
	}
}

// TestMemStore_LatestSnapshotForTier_NotFound confirms the ErrNotFound
// contract: schedd's tier-fallback chain relies on it to decide
// whether to fall through to the next tier vs. cold-boot.
func TestMemStore_LatestSnapshotForTier_NotFound(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c5")
	if _, err := m.LatestSnapshotForTier(ctx, "00000000-0000-0000-0000-0000000000c5", state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("empty warm lookup = %v, want ErrNotFound", err)
	}
	if _, err := m.LatestSnapshotForTier(ctx, "00000000-0000-0000-0000-0000000000c5", ""); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("empty tier = %v, want ErrNotFound", err)
	}
}

// TestMemStore_ListSnapshotsForGC_ProjectsTier confirms the GC
// projection now carries the tier so perAppKeepCurrentPrevious can
// keep (current warm + previous init) per warm-tier app (issue #470
// / ADR-055 bucket 4). A stale tier=init row is filtered out before
// projection, so it never reaches the GC loop.
func TestMemStore_ListSnapshotsForGC_ProjectsTier(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStoreForTest()
	seedTierAppAndDeployment(t, m, "00000000-0000-0000-0000-0000000000c6")

	initSnap, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c6", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c6/mem", Tier: state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: "00000000-0000-0000-0000-0000000000c6", FCVersion: "fc-1.0",
		StorageKey: "snap/00000000-0000-0000-0000-0000000000c6/warm/mem", Tier: state.SnapshotTierWarm,
	}); err != nil {
		t.Fatalf("seed warm: %v", err)
	}

	got, err := m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSnapshotsForGC len = %d, want 2 (init + warm)", len(got))
	}
	// Both rows must carry a Tier so the GC loop can branch.
	for _, r := range got {
		if r.Tier == "" {
			t.Errorf("row %s has empty Tier (GC projection dropped the column)", r.ID)
		}
	}
	// After marking init stale, the projection shrinks to one row.
	if err := m.MarkSnapshotStale(ctx, initSnap.ID); err != nil {
		t.Fatalf("MarkSnapshotStale: %v", err)
	}
	got, err = m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC (post-stale): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("post-stale ListSnapshotsForGC len = %d, want 1", len(got))
	}
	if got[0].Tier != state.SnapshotTierWarm {
		t.Errorf("post-stale Tier = %q, want warm", got[0].Tier)
	}
}
