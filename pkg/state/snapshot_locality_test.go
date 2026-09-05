package state_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSnapshotLocalityKeepsOriginSeparate(t *testing.T) {
	s := state.NewMemStore()
	checkSnapshotLocality(t, s, "locality-deployment")
}

func TestPgSnapshotLocalityKeepsOriginSeparate(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, dep := seedLiveDeploy(t, s, ctx, "locality")
	checkSnapshotLocality(t, s, dep)
}

type localityTestStore interface {
	state.Store
	state.SnapshotOriginStore
	state.SnapshotReplicaStore
	state.SnapshotLocalityStore
}

func checkSnapshotLocality(t *testing.T, s localityTestStore, dep string) {
	t.Helper()
	ctx := context.Background()
	newNode := func(name string) state.ComputeNode {
		n, err := s.CreateComputeNode(ctx, state.ComputeNode{
			Name: name, TargetURL: "tcp://" + name + ":50051", VPCPUs: 4, MemMB: 8192,
			MaxConcurrency: 3, AdmissionCeilingMB: 4096, VCPUBudget: api.VCPUSlots, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	origin, replica := newNode("locality-origin"), newNode("locality-replica")
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep, FCVersion: "1.10.0", StorageKey: state.SnapMemKey(dep)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSnapshotOrigin(ctx, snap.ID, origin.ID); err != nil {
		t.Fatal(err)
	}
	assertLocality := func(ready []string) {
		t.Helper()
		got, err := s.SnapshotLocalityFor(ctx, snap.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.OriginNodeID != origin.ID || !reflect.DeepEqual(got.ReadyNodeIDs, ready) {
			t.Fatalf("locality=%+v, want origin=%s ready=%v", got, origin.ID, ready)
		}
	}
	assertLocality(nil)
	if _, err := s.EnqueueSnapshotReplicasForNode(ctx, replica.ID); err != nil {
		t.Fatal(err)
	}
	assertLocality(nil) // Pending is not a verified local copy.
	if err := s.MarkSnapshotReplicaReady(ctx, snap.ID, replica.ID); err != nil {
		t.Fatal(err)
	}
	assertLocality([]string{replica.ID})
}
