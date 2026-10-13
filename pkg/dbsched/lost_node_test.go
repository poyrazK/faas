// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

// A lost node's databases are pruned in the bucket through a live node,
// with their own windows, at most hourly; databases on live nodes are left
// to their own node's dbbackupd.
func TestPruneLostArchives(t *testing.T) {
	ctx := context.Background()
	s, store, vmm, first := twoNodeScheduler(t, true)
	second, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 7200})
	if err != nil || second.NodeID != "db-a" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	onB, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || onB.NodeID != "db-b" {
		t.Fatalf("third = %+v, %v", onB, err)
	}
	if n, err := s.PruneLostArchives(ctx); err != nil || n != 0 {
		t.Fatalf("no node lost = %d, %v", n, err)
	}
	markNode(t, store, "db-a", NodeUnavailable)
	vmm.onNode = nil
	vmm.pruneErr = map[string]error{"db-b": errors.New("dbbackupd down")}
	if n, _ := s.PruneLostArchives(ctx); n != 0 {
		t.Fatalf("prune with the live node failing = %d", n)
	}
	vmm.pruneErr = nil
	vmm.onNode = nil
	if n, err := s.PruneLostArchives(ctx); err != nil || n != 2 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	calls := vmm.nodeCalls()
	for _, want := range []string{
		"prune:" + first.DatabaseID + ":0@db-b",
		"prune:" + second.DatabaseID + ":7200@db-b",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("calls %q lack %q", calls, want)
		}
	}
	if strings.Contains(calls, "@db-a") || strings.Contains(calls, onB.DatabaseID) {
		t.Fatalf("pruned through the lost node or a live node's database: %s", calls)
	}
	if n, _ := s.PruneLostArchives(ctx); n != 0 {
		t.Fatalf("pruned again within the hour: %d", n)
	}
	later := time.Now().Add(2 * time.Hour)
	s.now = func() time.Time { return later }
	if n, _ := s.PruneLostArchives(ctx); n != 2 {
		t.Fatalf("an hour later = %d", n)
	}
	s.cfg.SharedArchive = false
	s.now = func() time.Time { return later.Add(2 * time.Hour) }
	if n, _ := s.PruneLostArchives(ctx); n != 0 {
		t.Fatalf("without a shared archive = %d", n)
	}
}

// A database active on a lost node stops billing compute: its record parks
// (closing the compute interval) and its wakes fail until the node returns.
func TestSettleLostNodesStopsCompute(t *testing.T) {
	ctx := context.Background()
	s, store, _, db := twoNodeScheduler(t, true)
	hour := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	clock := hour.Add(5 * time.Minute)
	now := func() time.Time { return clock }
	store.now, s.now = now, now
	if _, err := s.Wake(ctx, db.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 0 {
		t.Fatalf("settle with no lost node = %d, %v", n, err)
	}
	clock = clock.Add(10 * time.Minute)
	markNode(t, store, "db-a", NodeUnavailable)
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v", n, err)
	}
	got, _ := store.Get(ctx, db.DatabaseID)
	if got.State != StateParked || got.LastError != errCodeNodeLost {
		t.Fatalf("record = %s/%s", got.State, got.LastError)
	}
	clock = hour.Add(2 * time.Hour)
	u, err := s.Usage(ctx, db.DatabaseID, hour, hour.Add(time.Hour))
	if err != nil || u.ActiveSeconds != 600 {
		t.Fatalf("usage = %+v, %v; want the 10 minutes before the loss only", u, err)
	}
	if _, err := s.Wake(ctx, db.DatabaseID); !errors.Is(err, ErrNodeUnavailable) {
		t.Fatalf("wake on the lost node = %v", err)
	}
}
