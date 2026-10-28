package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 21 H21-1: a superseded deployment inside the rollback window keeps its
// layers whatever state its snapshots are in, and a rollback to a target whose
// layers were reclaimed is refused before anything changes.
func TestMemRollbackWindowRetainsLayers(t *testing.T) {
	rollbackWindowRetainsLayers(t, state.NewMemStore())
}

// Hunt 21 H21-2: stale retention counts from the stale mark, not creation.
func TestMemStaleRetentionCountsFromTheStaleMark(t *testing.T) {
	staleRetentionCountsFromTheStaleMark(t, state.NewMemStore(), nil, nil)
}

func rollbackWindowRetainsLayers(t *testing.T, s layerRetentionTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "rollback-window-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rollback-window-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	// Four releases, oldest first; each supersedes the one before.
	base := time.Now().Add(-time.Hour)
	var deployments []state.Deployment
	var keys []string
	for i := range api.SnapshotRollbackRetentionDeployments + 1 {
		d, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: fmt.Sprintf("sha256:release-%d", i), CreatedAt: base.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("layers/rollback-window-%d-%s", i, d.ID)
		if err := s.SetDeploymentRootfs(ctx, d.ID, "/rollback-window.ext4", key, 1024); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		deployments, keys = append(deployments, d), append(keys, key)
	}
	for _, d := range deployments[:len(deployments)-1] {
		if got, err := s.DeploymentByID(ctx, d.ID); err != nil || got.Status != state.DeploySuperseded {
			t.Fatalf("release %s = %s (err %v), want superseded", d.ID, got.Status, err)
		}
	}

	// The live release and the two before it are the rollback window. The
	// superseded ones have no snapshot, which used to free their layers.
	for _, key := range keys[1 : len(keys)-1] {
		if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
			t.Fatalf("claim %s inside the rollback window = eligible %v, err %v; want retained", key, eligible, err)
		}
	}
	claim, eligible, err := s.ClaimLayerArtifactDeletion(ctx, keys[0])
	if err != nil || !eligible {
		t.Fatalf("claim %s outside the rollback window = eligible %v, err %v; want reclaimable", keys[0], eligible, err)
	}
	if err := s.CompleteLayerArtifactDeletion(ctx, claim); err != nil {
		t.Fatal(err)
	}

	// A rollback to the reclaimed release is refused and changes nothing.
	if _, err := s.PrepareDeploymentRollback(ctx, app.ID, deployments[0].ID); !errors.Is(err, state.ErrLayerArtifactRetired) {
		t.Fatalf("rollback to a reclaimed release = %v, want ErrLayerArtifactRetired", err)
	}
	if got, err := s.DeploymentByID(ctx, deployments[0].ID); err != nil || got.Status != state.DeploySuperseded {
		t.Fatalf("refused rollback target = %s (err %v), want still superseded", got.Status, err)
	}
	// A rollback inside the window still prepares.
	if prepared, err := s.PrepareDeploymentRollback(ctx, app.ID, deployments[1].ID); err != nil || prepared.Status != state.DeploySnapshotting {
		t.Fatalf("rollback inside the window = %s, err %v; want snapshotting", prepared.Status, err)
	}
}

// staleRetentionCountsFromTheStaleMark: a snapshot created long ago and
// marked stale now is kept for the retention window. backdateCreated sets the
// creation time when the store ignores it on insert; backdateStale, when set,
// moves the stale mark past the window.
func staleRetentionCountsFromTheStaleMark(t *testing.T, s layerRetentionTestStore, backdateCreated, backdateStale func(t *testing.T, id string)) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "stale-retention-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stale-retention-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stale-retention"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: d.ID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: state.SnapMemKey(d.ID), CreatedAt: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if backdateCreated != nil {
		backdateCreated(t, snap.ID)
	}
	if err := s.MarkSnapshotStale(ctx, snap.ID); err != nil {
		t.Fatal(err)
	}
	expired := func() bool {
		t.Helper()
		rows, err := s.ListSnapshotsStaleOlderThan(ctx, api.SnapshotStaleRetention)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == snap.ID {
				return true
			}
		}
		return false
	}
	if expired() {
		t.Fatal("a 30-day-old snapshot marked stale now is listed as past its stale retention")
	}
	if n, err := s.DeleteSnapshotsStaleOlderThan(ctx, api.SnapshotStaleRetention); err != nil || n != 0 {
		t.Fatalf("DeleteSnapshotsStaleOlderThan = %d, %v; want nothing deleted inside the retention", n, err)
	}
	if backdateStale == nil {
		return
	}
	backdateStale(t, snap.ID)
	if !expired() {
		t.Fatal("a snapshot stale for longer than the retention is not listed")
	}
	if n, err := s.DeleteSnapshotsStaleOlderThan(ctx, api.SnapshotStaleRetention); err != nil || n != 1 {
		t.Fatalf("DeleteSnapshotsStaleOlderThan = %d, %v; want the expired row deleted", n, err)
	}
}
