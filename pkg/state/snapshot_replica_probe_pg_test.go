package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgReconcileSnapshotReplicasReportsClaimableWork pins the idle-tick probe
// against the claim it lets the replica worker skip: in every replica state the
// probe says "claimable" exactly when ClaimSnapshotReplica would lease a job,
// including a retry or a stale lease that becomes due without any new fan-out
// event.
func TestPgReconcileSnapshotReplicasReportsClaimableWork(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	_, _, deploymentID := seedLiveDeploy(t, s, ctx, "replica-probe")
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID, FCVersion: "fc-probe", MemBytes: 1024, DiskBytes: 2048,
		StorageKey: state.SnapMemKey(deploymentID),
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql+` where snapshot_id = $1 and node_id = $2`, snap.ID, nodeID); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// agree probes, then claims, and requires the two to agree.
	agree := func(step string, want bool) state.SnapshotReplicaJob {
		t.Helper()
		_, claimable, err := s.ReconcileSnapshotReplicas(ctx, nodeID)
		if err != nil {
			t.Fatalf("%s: probe: %v", step, err)
		}
		job, claimErr := s.ClaimSnapshotReplica(ctx, nodeID)
		claimed := claimErr == nil
		if claimErr != nil && !errors.Is(claimErr, state.ErrNotFound) {
			t.Fatalf("%s: claim: %v", step, claimErr)
		}
		if claimable != want || claimed != want {
			t.Fatalf("%s: probe claimable=%t, claim found a job=%t; want both %t", step, claimable, claimed, want)
		}
		return job
	}

	job := agree("new fan-out event", true)
	agree("syncing under a live lease", false)
	if err := s.MarkSnapshotReplicaFailedWithLease(ctx, snap.ID, nodeID, job.LeaseToken, errors.New("registry unavailable")); err != nil {
		t.Fatal(err)
	}
	exec(`update snapshot_replicas set next_attempt_at = now() + interval '1 hour'`)
	agree("failed, retry not yet due", false)
	exec(`update snapshot_replicas set next_attempt_at = now() - interval '1 second'`)
	job = agree("failed, retry due", true)
	exec(`update snapshot_replicas set updated_at = now() - interval '6 minutes'`)
	job = agree("syncing with a stale lease", true)
	if err := s.MarkSnapshotReplicaReadyWithLease(ctx, snap.ID, nodeID, job.LeaseToken); err != nil {
		t.Fatal(err)
	}
	agree("ready", false)
	if added, err := s.EnqueueSnapshotReplicasForNode(ctx, nodeID); err != nil || added != 0 {
		t.Fatalf("caught-up enqueue = %d, %v; want 0, nil", added, err)
	}
}
