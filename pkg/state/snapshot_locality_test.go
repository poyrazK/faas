package state_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSnapshotLocalityTracksOriginResidency(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "locality@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "locality-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		ID: "locality-deployment", AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:locality", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	checkSnapshotLocality(t, s, dep.ID)
}

func TestPgSnapshotLocalityTracksOriginResidency(t *testing.T) {
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
	if _, err := s.EnqueueSnapshotReplicasForNode(ctx, origin.ID); err != nil {
		t.Fatal(err)
	}
	assertLocality(nil) // Origin is not advertised until its cache is checked.
	if err := s.MarkSnapshotReplicaReady(ctx, snap.ID, origin.ID); err != nil {
		t.Fatal(err)
	}
	assertLocality([]string{origin.ID})
	if _, err := s.EnqueueSnapshotReplicasForNode(ctx, replica.ID); err != nil {
		t.Fatal(err)
	}
	assertLocality([]string{origin.ID}) // Pending is not a verified local copy.
	if err := s.MarkSnapshotReplicaReady(ctx, snap.ID, replica.ID); err != nil {
		t.Fatal(err)
	}
	ready := []string{origin.ID, replica.ID}
	if ready[1] < ready[0] {
		ready[0], ready[1] = ready[1], ready[0]
	}
	assertLocality(ready)
}
