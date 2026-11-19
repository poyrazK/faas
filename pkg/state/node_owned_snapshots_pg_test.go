package state_test

// adr: 20261118160000

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgOwnedSnapshotReplicas: the owner lists its app's live snapshot,
// queues the replica it does not hold, leaves an evicted one alone until the
// wait has passed and then queues it again; a transient failure keeps its own
// retry.
func TestPgOwnedSnapshotReplicas(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	_, appID, deploymentID := seedLiveDeploy(t, s, ctx, "owned-replica")
	if _, err := pool.Exec(ctx, `update apps set node_id = $2 where id = $1`, appID, nodeID); err != nil {
		t.Fatal(err)
	}
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID, FCVersion: "fc-owned", MemBytes: 1024, DiskBytes: 2048,
		StorageKey: "snap/" + deploymentID + "/captures/c1/v2/mem",
	})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.NodeOwnedSnapshots(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !owned.Captures["snap/"+deploymentID+"/captures/c1/v2/"] || len(owned.Layers) != 1 {
		t.Fatalf("owned = %+v, want the app's capture and layer", owned)
	}
	// The fan-out may already have queued the snapshot; take it either way.
	if _, err := s.RequeueOwnedSnapshotReplicas(ctx, nodeID, time.Hour); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimSnapshotReplica(ctx, nodeID)
	if err != nil || job.SnapshotID != snap.ID {
		t.Fatalf("claim = %+v, %v; want the owned snapshot", job, err)
	}
	evicted := state.PermanentSnapshotReplicaError(errors.New("snapshothipd: replica evicted from the local cache"))
	if err := s.MarkSnapshotReplicaFailedWithLease(ctx, snap.ID, nodeID, job.LeaseToken, evicted); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RequeueOwnedSnapshotReplicas(ctx, nodeID, time.Hour); err != nil || n != 0 {
		t.Fatalf("requeue right after the eviction = %d, %v; want none", n, err)
	}
	if _, err := pool.Exec(ctx, `update snapshot_replicas set updated_at = now() - interval '2 hours'
		 where snapshot_id = $1 and node_id = $2`, snap.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RequeueOwnedSnapshotReplicas(ctx, nodeID, time.Hour); err != nil || n != 1 {
		t.Fatalf("requeue after the wait = %d, %v; want the evicted replica queued", n, err)
	}
	job, err = s.ClaimSnapshotReplica(ctx, nodeID)
	if err != nil || job.SnapshotID != snap.ID {
		t.Fatalf("claim after requeue = %+v, %v", job, err)
	}
	if err := s.MarkSnapshotReplicaFailedWithLease(ctx, snap.ID, nodeID, job.LeaseToken, errors.New("registry unavailable")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update snapshot_replicas set updated_at = now() - interval '2 hours'
		 where snapshot_id = $1 and node_id = $2`, snap.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RequeueOwnedSnapshotReplicas(ctx, nodeID, time.Hour); err != nil || n != 0 {
		t.Fatalf("requeue of a transient failure = %d, %v; want its own retry kept", n, err)
	}
}
