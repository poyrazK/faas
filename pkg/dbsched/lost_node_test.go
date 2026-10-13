// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func markNode(t *testing.T, store *MemStore, id, lifecycle string) {
	t.Helper()
	nodes, _ := store.Nodes(context.Background())
	for _, n := range nodes {
		if n.ID == id {
			n.Lifecycle = lifecycle
			seedNodes(t, store, n)
			return
		}
	}
	t.Fatalf("no node %s", id)
}

// Deleting a database on a lost node touches nothing there: the record
// goes, and with a shared archive another node drops the bucket copy.
func TestDeleteOnALostNode(t *testing.T) {
	ctx := context.Background()
	for _, shared := range []bool{true, false} {
		s, store, vmm, db := twoNodeScheduler(t, shared)
		markNode(t, store, "db-a", NodeUnavailable)
		vmm.onNode = nil
		if err := s.Delete(ctx, db.DatabaseID); err != nil {
			t.Fatalf("shared=%v: delete = %v", shared, err)
		}
		if _, err := store.Get(ctx, db.DatabaseID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("shared=%v: record survived: %v", shared, err)
		}
		calls := vmm.nodeCalls()
		if strings.Contains(calls, "@db-a") {
			t.Fatalf("shared=%v: the lost node was called: %s", shared, calls)
		}
		if shared != (calls == "drop_archive@db-b") {
			t.Fatalf("shared=%v: calls %q", shared, calls)
		}
	}
}

// With every node lost the archive cannot be dropped yet: the delete
// fails, stays deleting, and completes once a node is back.
func TestDeleteWithNoNodeLeftRetries(t *testing.T) {
	ctx := context.Background()
	s, store, _, db := twoNodeScheduler(t, true)
	markNode(t, store, "db-a", NodeUnavailable)
	markNode(t, store, "db-b", NodeUnavailable)
	if err := s.Delete(ctx, db.DatabaseID); !errors.Is(err, ErrNodeUnavailable) {
		t.Fatalf("delete with no node = %v, want ErrNodeUnavailable", err)
	}
	if got, _ := store.Get(ctx, db.DatabaseID); got.State != StateDeleting {
		t.Fatalf("record = %s, want deleting", got.State)
	}
	markNode(t, store, "db-b", NodeActive)
	if err := s.Delete(ctx, db.DatabaseID); err != nil {
		t.Fatal(err)
	}
}

// When a lost node returns, the volumes of databases deleted meanwhile are
// collected (stopped first if their VM still runs); live ones never are.
func TestCollectOrphansAfterTheNodeReturns(t *testing.T) {
	ctx := context.Background()
	s, store, vmm, gone := twoNodeScheduler(t, false)
	live := provision(t, s)
	if live.NodeID != "db-a" {
		t.Fatalf("second database on %s", live.NodeID)
	}
	vmm.running[gone.DatabaseID] = true // the VM outlived the partition
	markNode(t, store, "db-a", NodeUnavailable)
	if err := s.Delete(ctx, gone.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CollectOrphans(ctx); err != nil || n != 0 {
		t.Fatalf("collect while the node is lost = %d, %v", n, err)
	}
	markNode(t, store, "db-a", NodeActive)
	vmm.onNode = nil
	if n, err := s.CollectOrphans(ctx); err != nil || n != 1 {
		t.Fatalf("collect after return = %d, %v", n, err)
	}
	if calls := vmm.nodeCalls(); !strings.Contains(calls, "stop@db-a") || !strings.Contains(calls, "delete@db-a") {
		t.Fatalf("orphan removal calls = %s", calls)
	}
	if _, ok := vmm.volumes[gone.DatabaseID]; ok {
		t.Fatal("orphaned volume survived")
	}
	if _, ok := vmm.volumes[live.DatabaseID]; !ok {
		t.Fatal("a live database's volume was collected")
	}
	if n, _ := s.CollectOrphans(ctx); n != 0 {
		t.Fatalf("second collect = %d", n)
	}
}
