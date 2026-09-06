package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgSnapshotReplicaEventCursorAndOriginFiltering(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, _, deploymentID := seedLiveDeploy(t, s, ctx, "snapshot-events")

	first, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID,
		FCVersion:    "fc-test",
		MemBytes:     1024,
		DiskBytes:    2048,
		StorageKey:   state.SnapMemKey(deploymentID) + "/init",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot first: %v", err)
	}

	region := func(value string) *string { return &value }
	newNode := func(name, locality string) state.ComputeNode {
		return state.ComputeNode{
			Name:               name,
			TargetURL:          "tcp://" + name + ":50051",
			VPCPUs:             8,
			MemMB:              8192,
			MaxConcurrency:     100,
			AdmissionCeilingMB: 7000,
			VCPUBudget:         api.VCPUSlots,
			Active:             true,
			Region:             region(locality),
		}
	}
	nodeA, err := s.CreateComputeNode(ctx, newNode("snapshot-origin", "eu-fsn1"))
	if err != nil {
		t.Fatalf("CreateComputeNode origin: %v", err)
	}
	nodeB, err := s.CreateComputeNode(ctx, newNode("snapshot-peer", "eu-fsn1"))
	if err != nil {
		t.Fatalf("CreateComputeNode peer: %v", err)
	}
	if _, err := s.CreateComputeNode(ctx, newNode("snapshot-other-region", "us-east")); err != nil {
		t.Fatalf("CreateComputeNode other region: %v", err)
	}

	if err := s.RecordSnapshotOrigin(ctx, first.ID, nodeA.ID); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}

	var replicaCount, originCount, peerCount int
	if err := pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where node_id = $2),
		       count(*) filter (where node_id = $3)
		  from snapshot_replicas
		 where snapshot_id = $1`, first.ID, nodeA.ID, nodeB.ID).Scan(&replicaCount, &originCount, &peerCount); err != nil {
		t.Fatalf("read first snapshot replicas: %v", err)
	}
	if replicaCount != 2 || originCount != 1 || peerCount != 1 {
		t.Fatalf("first snapshot replicas = count %d origin %d peer %d, want one row per in-region node", replicaCount, originCount, peerCount)
	}

	// The origin trigger has already materialized the first event's eligible
	// row. The cursor still advances over that event without inserting a
	// duplicate, proving that a node can restart after trigger-based repair.
	if added, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeB.ID); err != nil {
		t.Fatalf("enqueue first event: %v", err)
	} else if added != 0 {
		t.Fatalf("enqueue first event added %d rows, want 0 because trigger already queued it", added)
	}

	var firstEventID, cursorID int64
	if err := pool.QueryRow(ctx, `
		select id from snapshot_fanout_events where snapshot_id = $1`, first.ID).Scan(&firstEventID); err != nil {
		t.Fatalf("read first fan-out event: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select last_event_id from snapshot_replica_cursors where node_id = $1`, nodeB.ID).Scan(&cursorID); err != nil {
		t.Fatalf("read first replica cursor: %v", err)
	}
	if cursorID != firstEventID {
		t.Fatalf("cursor = %d after first event, want %d", cursorID, firstEventID)
	}

	second, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID,
		FCVersion:    "fc-test",
		MemBytes:     1024,
		DiskBytes:    2048,
		StorageKey:   state.SnapMemKey(deploymentID) + "/warm",
		Tier:         state.SnapshotTierWarm,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot second: %v", err)
	}

	if added, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeB.ID); err != nil {
		t.Fatalf("enqueue second event: %v", err)
	} else if added != 1 {
		t.Fatalf("enqueue second event added %d rows, want 1", added)
	}
	if added, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeB.ID); err != nil {
		t.Fatalf("repeat enqueue: %v", err)
	} else if added != 0 {
		t.Fatalf("repeat enqueue added %d rows, want 0", added)
	}

	var secondEventID int64
	if err := pool.QueryRow(ctx, `
		select id from snapshot_fanout_events where snapshot_id = $1`, second.ID).Scan(&secondEventID); err != nil {
		t.Fatalf("read second fan-out event: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select last_event_id from snapshot_replica_cursors where node_id = $1`, nodeB.ID).Scan(&cursorID); err != nil {
		t.Fatalf("read final replica cursor: %v", err)
	}
	if cursorID != secondEventID {
		t.Fatalf("final cursor = %d, want %d", cursorID, secondEventID)
	}

	if err := s.SetDeploymentRootfs(ctx, deploymentID,
		"/srv/fc/apps/pg-app/"+deploymentID+".ext4",
		"apps/pg-app/"+deploymentID+".ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	if _, err := s.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: deploymentID, SidecarName: "metrics",
		StorageKey: "apps/pg-app/" + deploymentID + "-metrics.ext4", Bytes: 1024,
	}); err != nil {
		t.Fatalf("SetDeploymentSidecarLayer: %v", err)
	}
	job, err := s.ClaimSnapshotReplica(ctx, nodeB.ID)
	if err != nil {
		t.Fatalf("ClaimSnapshotReplica: %v", err)
	}
	if got, want := job.LayerStorageKeys, []string{
		"apps/pg-app/" + deploymentID + ".ext4",
		"apps/pg-app/" + deploymentID + "-metrics.ext4",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claimed layer keys = %v, want %v", got, want)
	}
	if err := s.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, nodeB.ID, errors.New("temporary registry outage")); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailed transient: %v", err)
	}
	if err := s.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, nodeB.ID,
		state.PermanentSnapshotReplicaError(errors.New("immutable object missing"))); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailed permanent: %v", err)
	}
}

func TestPgSnapshotReplicaClaimPrioritizesCustomerWakes(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)

	seed := func(suffix, slug string, status state.DeploymentStatus) state.Snapshot {
		t.Helper()
		_, _, deploymentID := seedLiveDeploy(t, s, ctx, suffix, slug)
		if err := s.UpdateDeploymentStatus(ctx, deploymentID, status, ""); err != nil {
			t.Fatalf("UpdateDeploymentStatus(%s): %v", status, err)
		}
		snap, err := s.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: deploymentID,
			FCVersion:    "fc-priority",
			MemBytes:     1024,
			DiskBytes:    2048,
			StorageKey:   state.SnapMemKey(deploymentID),
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", status, err)
		}
		return snap
	}

	// Insert rollback history first to prove queue age no longer outranks a
	// deployment that is about to serve or is already serving requests.
	rollback := seed("-replica-rollback", "replica-rollback", state.DeploySuperseded)
	live := seed("-replica-live", "replica-live", state.DeployLive)
	snapshotting := seed("-replica-snapshotting", "replica-snapshotting", state.DeploySnapshotting)

	added, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 {
		t.Fatalf("enqueued = %d, want 3", added)
	}
	for _, want := range []state.Snapshot{live, snapshotting, rollback} {
		job, err := s.ClaimSnapshotReplica(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if job.SnapshotID != want.ID {
			t.Fatalf("claimed %s, want %s", job.SnapshotID, want.ID)
		}
		if err := s.MarkSnapshotReplicaReady(ctx, job.SnapshotID, nodeID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPgSnapshotReplicaTransientRetryAndReadyRevalidation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	_, _, deploymentID := seedLiveDeploy(t, s, ctx, "replica-retry-revalidate")
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID, FCVersion: "fc-retry", MemBytes: 1024, DiskBytes: 2048,
		StorageKey: state.SnapMemKey(deploymentID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeID); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		update snapshot_replicas
		   set state = 'failed', attempts = 8, next_attempt_at = now() - interval '1 second'
		 where snapshot_id = $1 and node_id = $2`, snap.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimSnapshotReplica(ctx, nodeID)
	if err != nil {
		t.Fatalf("claim capped transient failure: %v", err)
	}
	if job.Attempts != 8 {
		t.Fatalf("capped attempts = %d, want 8", job.Attempts)
	}
	if err := s.MarkSnapshotReplicaFailed(ctx, snap.ID, nodeID, errors.New("registry unavailable")); err != nil {
		t.Fatal(err)
	}
	var nextAttemptPresent bool
	if err := pool.QueryRow(ctx, `
		select next_attempt_at is not null
		  from snapshot_replicas
		 where snapshot_id = $1 and node_id = $2`, snap.ID, nodeID).Scan(&nextAttemptPresent); err != nil {
		t.Fatal(err)
	}
	if !nextAttemptPresent {
		t.Fatal("transient failure became terminal at the attempt cap")
	}

	if err := s.MarkSnapshotReplicaReady(ctx, snap.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		update snapshot_replicas
		   set ready_at = now() - interval '6 minutes'
		 where snapshot_id = $1 and node_id = $2`, snap.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeID); err != nil {
		t.Fatal(err)
	} else if refreshed != 1 {
		t.Fatalf("revalidated = %d, want 1", refreshed)
	}
	if _, err := s.ClaimSnapshotReplica(ctx, nodeID); err != nil {
		t.Fatalf("claim revalidation: %v", err)
	}
}

func TestPgSnapshotReplicaLifecycleRetiresDeadSnapshots(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)

	assertRetired := func(snapshotID, deploymentID string) {
		t.Helper()
		var stale bool
		if err := pool.QueryRow(ctx, `select stale from snapshots where id = $1`, snapshotID).Scan(&stale); err != nil {
			t.Fatal(err)
		}
		if !stale {
			t.Fatalf("snapshot %s remained fresh", snapshotID)
		}
		var replicas int
		if err := pool.QueryRow(ctx, `select count(*) from snapshot_replicas where snapshot_id = $1`, snapshotID).Scan(&replicas); err != nil {
			t.Fatal(err)
		}
		if replicas != 0 {
			t.Fatalf("snapshot %s retained %d replica rows", snapshotID, replicas)
		}
		if _, err := s.LatestSnapshot(ctx, deploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("LatestSnapshot(%s) = %v, want ErrNotFound", deploymentID, err)
		}
	}

	_, _, failedDeploymentID := seedLiveDeploy(t, s, ctx, "-replica-failed", "replica-failed")
	failedSnapshot, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: failedDeploymentID, FCVersion: "fc-retire", MemBytes: 1024,
		DiskBytes: 2048, StorageKey: state.SnapMemKey(failedDeploymentID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, failedDeploymentID, state.DeployFailed, "failed after snapshot"); err != nil {
		t.Fatal(err)
	}
	assertRetired(failedSnapshot.ID, failedDeploymentID)

	_, deletedAppID, deletedDeploymentID := seedLiveDeploy(t, s, ctx, "-replica-deleted", "replica-deleted")
	deletedSnapshot, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deletedDeploymentID, FCVersion: "fc-retire", MemBytes: 1024,
		DiskBytes: 2048, StorageKey: state.SnapMemKey(deletedDeploymentID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDeleteAppCascade(ctx, deletedAppID); err != nil {
		t.Fatal(err)
	}
	assertRetired(deletedSnapshot.ID, deletedDeploymentID)
}
