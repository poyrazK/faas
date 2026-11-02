// adr: 909, 20261101091815 — each node's own scheduler publishes the node's
// measured memory, and peers admit, place and handle pressure by it.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodeMemoryReportsPublishTheOwnNodeAndServePeers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	local, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "peer", AdmissionCeilingMB: 8000, VCPUBudget: 24, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID(local.ID)
	e.EnableNodeMemoryReports(func(nodeID string, at time.Time) (int, int, time.Time, bool) {
		return 900, 2, at, nodeID == local.ID
	})
	now := time.Now()
	if err := store.UpsertNodeMemoryReport(ctx, state.NodeMemoryReport{NodeID: peer.ID, MeasuredMB: 3000, FrozenInstances: 7, MeasuredAt: now}); err != nil {
		t.Fatal(err)
	}
	e.ReportNodeMemory(ctx, now)

	reports, err := store.NodeMemoryReports(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	published := false
	for _, r := range reports {
		published = published || r.NodeID == local.ID && r.MeasuredMB == 900 && r.FrozenInstances == 2
	}
	if !published {
		t.Fatalf("reports = %+v, missing this scheduler's own node", reports)
	}
	if r, ok := e.PeerNodeMemory(peer.ID, now); !ok || r.MeasuredMB != 3000 {
		t.Fatalf("PeerNodeMemory(peer) = %+v, %v; want the published 3000 MB", r, ok)
	}
	if _, ok := e.PeerNodeMemory(local.ID, now); ok {
		t.Fatal("the own node was served as a peer; it is measured locally")
	}
	stale := now.Add(time.Duration(api.NodeMemoryPeerMaxAgeSeconds)*time.Second + time.Second)
	if _, ok := e.PeerNodeMemory(peer.ID, stale); ok {
		t.Fatal("a stale peer report was served")
	}
	// FrozenMaxPerNode counts every owner's frozen VMs on the node.
	if got := e.NodeFrozenCount(peer.ID); got != 7 {
		t.Fatalf("NodeFrozenCount(peer) = %d, want the report's 7", got)
	}
}

// Placement on a node without a vmmd report (a peer, or a stream gap) used the
// node's bookings against 100% of its ceiling. With a measured basis it uses
// what Admit uses, still bounded by the bookings' overcommit cap.
func TestPlacementOnAnUnreportedNodeUsesItsMeasurement(t *testing.T) {
	e := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	node := state.ComputeNode{ID: "peer", AdmissionCeilingMB: 8000}
	if got := e.UnreportedNodeUsedMB(node, 9000); got != 9000 {
		t.Fatalf("without a measurement used = %d, want the 9000 MB bookings", got)
	}
	asOf := time.Now()
	e.Ledger().EnableMeasuredAdmission(func(nodeID string) (int, time.Time, bool) {
		return 2000, asOf, nodeID == "peer"
	})
	// 9000 MB booked is 1000 MB of the 8000 MB overcommit allowance.
	if got := e.UnreportedNodeUsedMB(node, 9000); got != 2000 {
		t.Fatalf("measured used = %d, want the 2000 MB measurement", got)
	}
	// 15000 MB booked leaves 7000 MB under the 200% cap: the cap binds.
	if got := e.UnreportedNodeUsedMB(node, 15000); got != 7000 {
		t.Fatalf("near the cap used = %d, want 7000", got)
	}
}
