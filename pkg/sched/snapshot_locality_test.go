package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeSnapshotProducerLocality(t *testing.T) {
	for _, tc := range []struct {
		name         string
		origin       bool
		originFull   bool
		originOff    bool
		originNoCPU  bool
		warm         string
		spread       bool
		wantProducer bool
	}{
		{name: "first wake prefers producer over larger ready replica", origin: true, wantProducer: true},
		{name: "warm producer survives replica filtering", origin: true, warm: "producer", wantProducer: true},
		{name: "known warm replica remains preferred", origin: true, warm: "replica"},
		{name: "stale warm hint replaced by producer", origin: true, warm: "gone", wantProducer: true},
		{name: "full producer falls back", origin: true, originFull: true},
		{name: "drained producer falls back", origin: true, originOff: true},
		{name: "CPU admission preserved", origin: true, originNoCPU: true},
		{name: "legacy snapshot uses ready replica"},
		{name: "burst siblings retain spread", origin: true, spread: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			producer, replica := seedTwoNodes(t, store)
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			snap, err := store.CreateSnapshot(ctx, state.Snapshot{
				DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: state.SnapMemKey(dep.ID),
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.origin {
				if err := store.RecordSnapshotOrigin(ctx, snap.ID, producer); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.EnqueueSnapshotReplicasForNode(ctx, replica); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkSnapshotReplicaReady(ctx, snap.ID, replica); err != nil {
				t.Fatal(err)
			}
			node, err := store.ComputeNodeByID(ctx, producer)
			if err != nil {
				t.Fatal(err)
			}
			node.AdmissionCeilingMB = 1024 // Replica advertises much more headroom.
			if tc.originNoCPU {
				node.VCPUBudget = 1
			}
			if _, err := store.UpsertComputeNode(ctx, node); err != nil {
				t.Fatal(err)
			}
			if tc.originOff {
				if err := store.SetComputeNodeActive(ctx, producer, false); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithWarmAffinity(NewWarmAffinity(time.Minute))
			if tc.originFull {
				e.capacityTable.Replace(CapacityReport{NodeID: producer, UsedMB: 1024})
			}
			warm := map[string]string{"producer": producer, "replica": replica, "gone": "removed-node"}[tc.warm]
			if warm != "" {
				e.warmAffinity.RecordWake(app.ID, warm)
			}
			if tc.spread {
				ctx = WithBurstPlacementSpread(ctx)
			}
			got, err := e.Wake(ctx, app.ID, "", "", "gateway")
			if err != nil {
				t.Fatal(err)
			}
			want := replica
			if tc.wantProducer {
				want = producer
			}
			if got.NodeID != want {
				t.Fatalf("wake node = %s, want %s", got.NodeID, want)
			}
			if vmm.restores != 1 || vmm.coldBoots != 0 {
				t.Fatalf("restores=%d cold boots=%d", vmm.restores, vmm.coldBoots)
			}
		})
	}
}

type failedSnapshotLocalityStore struct{ state.Store }

func (failedSnapshotLocalityStore) SnapshotLocalityFor(context.Context, string) (state.SnapshotLocality, error) {
	return state.SnapshotLocality{}, errors.New("locality unavailable")
}

func TestSnapshotLocalityReadFailurePreservesFallback(t *testing.T) {
	store := failedSnapshotLocalityStore{Store: state.NewMemStore()}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	warm, replicas := e.snapshotPlacementHints(context.Background(), "snapshot", "last-warm-node")
	if warm != "last-warm-node" || len(replicas) != 0 {
		t.Fatalf("warm=%q replicas=%v", warm, replicas)
	}
}
