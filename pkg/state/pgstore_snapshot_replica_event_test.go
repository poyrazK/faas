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

	var replicaCount int
	var replicaNodeID string
	if err := pool.QueryRow(ctx, `
		select count(*), coalesce(min(node_id::text), '')
		  from snapshot_replicas
		 where snapshot_id = $1`, first.ID).Scan(&replicaCount, &replicaNodeID); err != nil {
		t.Fatalf("read first snapshot replicas: %v", err)
	}
	if replicaCount != 1 || replicaNodeID != nodeB.ID {
		t.Fatalf("first snapshot replicas = count %d node %q, want peer %q only", replicaCount, replicaNodeID, nodeB.ID)
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
