package state

// adr: 20261118160000

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMemStoreOwnedSnapshotReplicas: a node lists the live snapshot of an app
// it owns and nothing of an app another node owns; it queues the owned
// snapshot it has no replica of, leaves a ready or transiently failed replica
// alone, and fetches an evicted one back only once the wait has passed.
func TestMemStoreOwnedSnapshotReplicas(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	region := DefaultLocalityLabel
	owner := ComputeNode{ID: "node-owner", Name: "compute-owner", TargetURL: "unix:///run/faas/owner.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region}
	other := ComputeNode{ID: "node-other", Name: "compute-other", TargetURL: "unix:///run/faas/other.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region}
	for _, n := range []ComputeNode{owner, other} {
		if _, err := m.CreateComputeNode(ctx, n); err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
	}
	ownedApp, ownedDep := seedMemReplicaDeployment(t, m, "dep-owned", "owned-app")
	otherApp, otherDep := seedMemReplicaDeployment(t, m, "dep-other", "other-app")
	for app, node := range map[string]string{ownedApp.ID: owner.ID, otherApp.ID: other.ID} {
		if err := m.SetAppNodeID(ctx, app, node); err != nil {
			t.Fatalf("SetAppNodeID: %v", err)
		}
	}
	for _, dep := range []Deployment{ownedDep, otherDep} {
		if _, err := m.CreateSnapshot(ctx, Snapshot{ID: "snap-" + dep.ID, DeploymentID: dep.ID, FCVersion: "fc-1",
			StorageKey: "snap/" + dep.ID + "/captures/c1/v2/mem"}); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
	}

	got, err := m.NodeOwnedSnapshots(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Captures["snap/dep-owned/captures/c1/v2/"] || len(got.Captures) != 1 {
		t.Fatalf("owned captures = %v, want only dep-owned's", got.Captures)
	}
	if !got.Layers["layers/dep-owned.ext4"] || len(got.Layers) != 1 {
		t.Fatalf("owned layers = %v, want only dep-owned's", got.Layers)
	}

	queued, err := m.RequeueOwnedSnapshotReplicas(ctx, owner.ID, time.Hour)
	if err != nil || queued != 1 {
		t.Fatalf("first requeue = %d, %v; want the missing owned replica queued", queued, err)
	}
	if _, ok := m.snapshotReplicas[snapshotReplicaKey{snapshotID: "snap-dep-other", nodeID: owner.ID}]; ok {
		t.Fatal("queued a snapshot of an app another node owns")
	}
	job, err := m.ClaimSnapshotReplica(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ClaimSnapshotReplica: %v", err)
	}
	evicted := PermanentSnapshotReplicaError(errors.New("snapshothipd: replica evicted from the local cache"))
	if err := m.MarkSnapshotReplicaFailedWithLease(ctx, job.SnapshotID, owner.ID, job.LeaseToken, evicted); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailedWithLease: %v", err)
	}
	if queued, err := m.RequeueOwnedSnapshotReplicas(ctx, owner.ID, time.Hour); err != nil || queued != 0 {
		t.Fatalf("requeue right after the eviction = %d, %v; want none before the wait", queued, err)
	}
	key := snapshotReplicaKey{snapshotID: job.SnapshotID, nodeID: owner.ID}
	row := m.snapshotReplicas[key]
	row.updatedAt = time.Now().Add(-2 * time.Hour)
	m.snapshotReplicas[key] = row
	if queued, err := m.RequeueOwnedSnapshotReplicas(ctx, owner.ID, time.Hour); err != nil || queued != 1 {
		t.Fatalf("requeue after the wait = %d, %v; want the evicted replica queued again", queued, err)
	}
	if st := m.snapshotReplicas[key].state; st != SnapshotReplicaPending {
		t.Fatalf("requeued replica state = %s, want pending", st)
	}
	if queued, err := m.RequeueOwnedSnapshotReplicas(ctx, owner.ID, time.Hour); err != nil || queued != 0 {
		t.Fatalf("requeue of a pending replica = %d, %v; want none", queued, err)
	}
}
