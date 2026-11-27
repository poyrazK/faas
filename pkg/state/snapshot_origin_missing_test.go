package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Prod hunt r72: a snapshot whose storage object was gone failed fan-out on
// every node every hour, forever. SnapshotsMissingFromOrigin finds it once it
// is past its upload grace, and only for a missing-object failure.
func TestMemStoreSnapshotsMissingFromOrigin(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	region := DefaultLocalityLabel
	second := ComputeNode{
		ID: "node-2", Name: "compute-2", TargetURL: "unix:///run/faas/compute-2.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, second); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	fail := func(depID, slug string, cause error) Snapshot {
		_, dep := seedMemReplicaDeployment(t, m, depID, slug)
		snap, err := m.CreateSnapshot(ctx, Snapshot{ID: "snap-" + depID, DeploymentID: dep.ID, FCVersion: "fc-1", StorageKey: SnapMemKey(dep.ID)})
		if err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		if _, err := m.EnqueueSnapshotReplicasForNode(ctx, second.ID); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		job, err := m.ClaimSnapshotReplica(ctx, second.ID)
		if err != nil || job.SnapshotID != snap.ID {
			t.Fatalf("Claim = %+v, %v", job, err)
		}
		if err := m.MarkSnapshotReplicaFailedWithLease(ctx, snap.ID, second.ID, job.LeaseToken, cause); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		return snap
	}
	missing := fail("dep-missing", "origin-missing", SnapshotOriginMissingError(errors.New("storage: object not found")))
	evicted := fail("dep-evicted", "origin-evicted", PermanentSnapshotReplicaError(errors.New("replica evicted")))
	transient := fail("dep-transient", "origin-transient", errors.New("connection reset"))

	if got, _ := m.SnapshotsMissingFromOrigin(ctx, time.Hour, 10); len(got) != 0 {
		t.Fatalf("a fresh capture inside its grace was returned: %v", got)
	}
	m.mu.Lock()
	for i := range m.snapshots {
		m.snapshots[i].CreatedAt = time.Now().Add(-2 * time.Hour)
	}
	m.mu.Unlock()
	got, err := m.SnapshotsMissingFromOrigin(ctx, time.Hour, 10)
	if err != nil {
		t.Fatalf("SnapshotsMissingFromOrigin: %v", err)
	}
	if want := []string{missing.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (evicted %s and transient %s are not missing objects)", got, want, evicted.ID, transient.ID)
	}
	if err := m.MarkSnapshotStale(ctx, missing.ID); err != nil {
		t.Fatalf("MarkSnapshotStale: %v", err)
	}
	if got, _ := m.SnapshotsMissingFromOrigin(ctx, time.Hour, 10); len(got) != 0 {
		t.Fatalf("a stale snapshot was returned again: %v", got)
	}
}

// A NotFound recorded while the capture was still inside its grace (its
// upload may have been landing) does not retire it.
func TestMemStoreSnapshotsMissingFromOriginIgnoresEarlyFailures(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	now := time.Now()
	m.snapshots = []Snapshot{{ID: "snap-early", StorageKey: "snapshots/early/mem", CreatedAt: now.Add(-90 * time.Minute)}}
	m.snapshotReplicas = map[snapshotReplicaKey]snapshotReplicaRow{
		{snapshotID: "snap-early", nodeID: "node-2"}: {state: SnapshotReplicaFailed, lastError: snapshotOriginMissingPrefix + "not found", updatedAt: now.Add(-80 * time.Minute)},
	}
	if got, _ := m.SnapshotsMissingFromOrigin(ctx, time.Hour, 10); len(got) != 0 {
		t.Fatalf("an early failure retired the snapshot: %v", got)
	}
}
