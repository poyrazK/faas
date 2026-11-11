// adr: 1027 amendment 2026-11-11 — read replicas: a running database's
// standby takes its read sessions only while it runs, holds every
// acknowledged commit and sits on an active node.
package dbsched

import (
	"context"
	"testing"
)

func TestReplicaTargetFollowsAServingStandby(t *testing.T) {
	ctx := context.Background()
	s, store, _ := newStandbyScheduler(t, "db-a", "db-b")
	vm := wakeReplicated(t, s)
	id := vm.DatabaseID
	if vm.NodeTarget != "unix:///db-a" || vm.ReplicaTarget != "unix:///db-b" {
		t.Fatalf("Get = node %q replica %q; want the primary on db-a and the replica on db-b", vm.NodeTarget, vm.ReplicaTarget)
	}
	if woke, err := s.Wake(ctx, id); err != nil || woke.ReplicaTarget != "unix:///db-b" {
		t.Fatalf("Wake of a running database = replica %q, %v; want db-b", woke.ReplicaTarget, err)
	}

	// A standby that may miss acknowledged commits serves no reads.
	if err := store.MarkStandbySynced(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, id); got.ReplicaTarget != "" || got.NodeTarget != "unix:///db-a" {
		t.Fatalf("unsynchronized standby: replica %q node %q; want no replica", got.ReplicaTarget, got.NodeTarget)
	}
	if err := store.MarkStandbySynced(ctx, id, true); err != nil {
		t.Fatal(err)
	}

	// Nor does one on a node that drains.
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining})
	if got, _ := s.Get(ctx, id); got.ReplicaTarget != "" {
		t.Fatalf("standby on a draining node: replica %q; want none", got.ReplicaTarget)
	}
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeActive})

	// Nor a stopped one: a parked database's standby is parked too.
	if err := s.Park(ctx, id); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := s.Get(ctx, id); got.ReplicaTarget != "" {
		t.Fatalf("parked database: replica %q; want none", got.ReplicaTarget)
	}
}

func TestDatabasesWithoutAStandbyNameNoReplica(t *testing.T) {
	s, _, _ := newTestScheduler(t, 4096)
	db := provision(t, s)
	vm, err := s.Wake(context.Background(), db.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	if vm.NodeTarget == "" || vm.ReplicaTarget != "" {
		t.Fatalf("Wake = node %q replica %q; want the node only", vm.NodeTarget, vm.ReplicaTarget)
	}
}
