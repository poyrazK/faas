package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func seedMemReplicaDeployment(t *testing.T, m *MemStore, id, slug string) (App, Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, slug+"@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: slug, RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		ID: id, AppID: app.ID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:" + id, Status: DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := m.UpdateDeploymentStatus(ctx, dep.ID, DeployLive, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus(live): %v", err)
	}
	dep.Status = DeployLive
	return app, dep
}

func TestMemStoreSnapshotReplicaLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	region := DefaultLocalityLabel
	second := ComputeNode{
		ID: "node-2", Name: "compute-2", TargetURL: "unix:///run/faas/compute-2.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true,
		Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, second); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	_, dep := seedMemReplicaDeployment(t, m, "dep-1", "replica-lifecycle")
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-1", DeploymentID: dep.ID, FCVersion: "fc-1", StorageKey: SnapMemKey(dep.ID),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	local, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	if err := m.RecordSnapshotOrigin(ctx, snap.ID, local.ID); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}
	added, err := m.EnqueueSnapshotReplicasForNode(ctx, second.ID)
	if err != nil {
		t.Fatalf("EnqueueSnapshotReplicasForNode: %v", err)
	}
	if added != 1 {
		t.Fatalf("enqueued = %d, want 1", added)
	}
	job, err := m.ClaimSnapshotReplica(ctx, second.ID)
	if err != nil {
		t.Fatalf("ClaimSnapshotReplica: %v", err)
	}
	if job.SnapshotID != snap.ID || job.VMStateStorageKey != SnapVMStateKey(dep.ID) {
		t.Fatalf("job = %+v", job)
	}
	if got, want := job.LayerStorageKeys, []string{"layers/" + dep.ID + ".ext4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("job layer keys = %v, want %v", got, want)
	}
	if err := m.MarkSnapshotReplicaReady(ctx, snap.ID, second.ID); err != nil {
		t.Fatalf("MarkSnapshotReplicaReady: %v", err)
	}
	ready, err := m.ReadySnapshotReplicaNodes(ctx, snap.ID)
	if err != nil {
		t.Fatalf("ReadySnapshotReplicaNodes: %v", err)
	}
	if len(ready) != 1 || ready[0] != second.ID {
		t.Fatalf("ready nodes = %v, want [%s]", ready, second.ID)
	}
	if _, err := m.ClaimSnapshotReplica(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim err = %v, want ErrNotFound", err)
	}
}

func TestMemStoreSnapshotReplicaPermanentFailureStopsRetry(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	region := DefaultLocalityLabel
	second := ComputeNode{
		ID: "node-permanent", Name: "compute-permanent", TargetURL: "unix:///run/faas/compute-permanent.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true,
		Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, second); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	_, dep := seedMemReplicaDeployment(t, m, "dep-permanent", "replica-permanent")
	dep.RootfsKey = "apps/example/dep-permanent.ext4"
	m.deployments[dep.ID] = dep
	if _, err := m.SetDeploymentSidecarLayer(ctx, DeploymentSidecarLayer{
		DeploymentID: "dep-permanent", SidecarName: "z-sidecar", StorageKey: "apps/example/dep-permanent-z.ext4",
	}); err != nil {
		t.Fatalf("SetDeploymentSidecarLayer: %v", err)
	}
	if _, err := m.SetDeploymentSidecarLayer(ctx, DeploymentSidecarLayer{
		DeploymentID: "dep-permanent", SidecarName: "a-sidecar", StorageKey: "apps/example/dep-permanent-a.ext4",
	}); err != nil {
		t.Fatalf("SetDeploymentSidecarLayer: %v", err)
	}
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-permanent", DeploymentID: "dep-permanent", FCVersion: "fc-1", StorageKey: SnapMemKey("dep-permanent"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	local, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	if err := m.RecordSnapshotOrigin(ctx, snap.ID, local.ID); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}
	if _, err := m.EnqueueSnapshotReplicasForNode(ctx, second.ID); err != nil {
		t.Fatalf("EnqueueSnapshotReplicasForNode: %v", err)
	}
	job, err := m.ClaimSnapshotReplica(ctx, second.ID)
	if err != nil {
		t.Fatalf("ClaimSnapshotReplica: %v", err)
	}
	wantLayers := []string{
		"apps/example/dep-permanent.ext4",
		"apps/example/dep-permanent-a.ext4",
		"apps/example/dep-permanent-z.ext4",
	}
	if !reflect.DeepEqual(job.LayerStorageKeys, wantLayers) {
		t.Fatalf("job layer keys = %v, want %v", job.LayerStorageKeys, wantLayers)
	}
	if err := m.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, second.ID, errors.New("registry unavailable")); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailed transient: %v", err)
	}
	if err := m.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, second.ID,
		PermanentSnapshotReplicaError(errors.New("immutable layer missing"))); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailed permanent: %v", err)
	}
	if _, err := m.ClaimSnapshotReplica(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim after permanent failure = %v, want ErrNotFound", err)
	}
}

func TestMemStoreSnapshotReplicaTransientFailureKeepsRetryingAtCap(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	region := DefaultLocalityLabel
	node := ComputeNode{
		ID: "node-transient", Name: "compute-transient", TargetURL: "unix:///run/faas/compute-transient.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	_, dep := seedMemReplicaDeployment(t, m, "dep-transient", "replica-transient")
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-transient", DeploymentID: dep.ID, FCVersion: "fc-1", StorageKey: SnapMemKey(dep.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnqueueSnapshotReplicasForNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	key := snapshotReplicaKey{snapshotID: snap.ID, nodeID: node.ID}
	for attempt := 1; attempt <= snapshotReplicaAttemptCap+2; attempt++ {
		job, err := m.ClaimSnapshotReplica(ctx, node.ID)
		if err != nil {
			t.Fatalf("claim attempt %d: %v", attempt, err)
		}
		if err := m.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, node.ID, errors.New("registry unavailable")); err != nil {
			t.Fatal(err)
		}
		row := m.snapshotReplicas[key]
		if row.nextAttemptAt.IsZero() {
			t.Fatalf("attempt %d became terminal", attempt)
		}
		row.nextAttemptAt = time.Now().Add(-time.Second)
		m.snapshotReplicas[key] = row
	}
	if got := m.snapshotReplicas[key].attempts; got != snapshotReplicaAttemptCap {
		t.Fatalf("attempt counter = %d, want capped %d", got, snapshotReplicaAttemptCap)
	}
}

func TestMemStoreSnapshotReplicaReadyRowsAreRevalidated(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	region := DefaultLocalityLabel
	node := ComputeNode{
		ID: "node-revalidate", Name: "compute-revalidate", TargetURL: "unix:///run/faas/compute-revalidate.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	_, dep := seedMemReplicaDeployment(t, m, "dep-revalidate", "replica-revalidate")
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-revalidate", DeploymentID: dep.ID, FCVersion: "fc-1", StorageKey: SnapMemKey(dep.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnqueueSnapshotReplicasForNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	job, err := m.ClaimSnapshotReplica(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkSnapshotReplicaReady(ctx, job.SnapshotID, node.ID); err != nil {
		t.Fatal(err)
	}
	key := snapshotReplicaKey{snapshotID: snap.ID, nodeID: node.ID}
	row := m.snapshotReplicas[key]
	row.readyAt = time.Now().Add(-snapshotReplicaRevalidateAfter - time.Second)
	m.snapshotReplicas[key] = row
	if refreshed, err := m.EnqueueSnapshotReplicasForNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	} else if refreshed != 1 {
		t.Fatalf("revalidated = %d, want 1", refreshed)
	}
	if _, err := m.ClaimSnapshotReplica(ctx, node.ID); err != nil {
		t.Fatalf("claim revalidation: %v", err)
	}
}

func TestMemStoreSnapshotReplicaPrioritizesCustomerWakes(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	region := DefaultLocalityLabel
	peer := ComputeNode{
		ID: "priority-peer", Name: "priority-peer", TargetURL: "unix:///priority-peer.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, peer); err != nil {
		t.Fatal(err)
	}

	type candidate struct {
		status DeploymentStatus
		id     string
		slug   string
	}
	candidates := []candidate{
		{status: DeploySuperseded, id: "dep-rollback", slug: "replica-rollback"},
		{status: DeployLive, id: "dep-live", slug: "replica-live"},
		{status: DeploySnapshotting, id: "dep-snapshotting", slug: "replica-snapshotting"},
		{status: DeployFailed, id: "dep-failed", slug: "replica-failed"},
	}
	for i, candidate := range candidates {
		_, dep := seedMemReplicaDeployment(t, m, candidate.id, candidate.slug)
		// Set the map directly so the fixture can model a pre-migration dead
		// row without the new lifecycle stale hook repairing it first.
		dep.Status = candidate.status
		m.deployments[dep.ID] = dep
		if _, err := m.CreateSnapshot(ctx, Snapshot{
			ID: "snap-" + candidate.id, DeploymentID: dep.ID, FCVersion: "fc-1",
			StorageKey: SnapMemKey(dep.ID), CreatedAt: time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatal(err)
		}
	}
	deletedApp, deletedDep := seedMemReplicaDeployment(t, m, "dep-deleted", "replica-deleted")
	deletedApp.Status = AppDeleted
	m.apps[deletedApp.ID] = deletedApp
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-deleted", DeploymentID: deletedDep.ID, FCVersion: "fc-1",
		StorageKey: SnapMemKey(deletedDep.ID), CreatedAt: time.Unix(5, 0),
	}); err != nil {
		t.Fatal(err)
	}

	added, err := m.EnqueueSnapshotReplicasForNode(ctx, peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 {
		t.Fatalf("enqueued = %d, want 3 serviceable snapshots", added)
	}
	for _, want := range []string{"snap-dep-live", "snap-dep-snapshotting", "snap-dep-rollback"} {
		job, err := m.ClaimSnapshotReplica(ctx, peer.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.SnapshotID != want {
			t.Fatalf("claimed %q, want %q", job.SnapshotID, want)
		}
		if err := m.MarkSnapshotReplicaReady(ctx, job.SnapshotID, peer.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMemStoreSnapshotReplicaRetiresTerminalAndDeletedSnapshots(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	region := DefaultLocalityLabel
	peer := ComputeNode{
		ID: "retirement-peer", Name: "retirement-peer", TargetURL: "unix:///retirement-peer.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, peer); err != nil {
		t.Fatal(err)
	}
	makeReadyReplica := func() {
		t.Helper()
		added, err := m.EnqueueSnapshotReplicasForNode(ctx, peer.ID)
		if err != nil {
			t.Fatal(err)
		}
		if added != 1 {
			t.Fatalf("enqueued = %d, want 1", added)
		}
		job, err := m.ClaimSnapshotReplica(ctx, peer.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.MarkSnapshotReplicaReady(ctx, job.SnapshotID, peer.ID); err != nil {
			t.Fatal(err)
		}
	}
	assertNoReadyReplica := func(snapshotID string) {
		t.Helper()
		got, err := m.SnapshotLocalityFor(ctx, snapshotID)
		if err != nil || got.OriginNodeID != "" || len(got.ReadyNodeIDs) != 0 {
			t.Fatalf("retired snapshot locality = %+v, err=%v", got, err)
		}
	}

	_, failedDep := seedMemReplicaDeployment(t, m, "dep-terminal", "replica-terminal")
	failedSnap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-terminal", DeploymentID: failedDep.ID, FCVersion: "fc-1", StorageKey: SnapMemKey(failedDep.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	makeReadyReplica()
	if err := m.UpdateDeploymentStatus(ctx, failedDep.ID, DeployFailed, "failed after snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LatestSnapshot(ctx, failedDep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed deployment snapshot remained usable: %v", err)
	}
	assertNoReadyReplica(failedSnap.ID)

	deletedApp, deletedDep := seedMemReplicaDeployment(t, m, "dep-delete", "replica-delete")
	deletedSnap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-delete", DeploymentID: deletedDep.ID, FCVersion: "fc-1", StorageKey: SnapMemKey(deletedDep.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	makeReadyReplica()
	if _, err := m.SoftDeleteAppCascade(ctx, deletedApp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LatestSnapshot(ctx, deletedDep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted app snapshot remained usable: %v", err)
	}
	assertNoReadyReplica(deletedSnap.ID)
}

func TestSnapshotReplicaRetryDelayIsCapped(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 5 * time.Second},
		{attempt: 1, want: 5 * time.Second},
		{attempt: 2, want: 10 * time.Second},
		{attempt: 7, want: 5 * time.Minute},
		{attempt: 100, want: 5 * time.Minute},
	}
	for _, tt := range tests {
		if got := snapshotReplicaRetryDelay(tt.attempt); got != tt.want {
			t.Errorf("snapshotReplicaRetryDelay(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}
