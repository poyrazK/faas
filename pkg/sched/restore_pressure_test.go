// spec: §6.2
// adr: 025
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRestorePressurePlacementPreservesLocalityUntilWatermark(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("cached"), cpuPlacementNode("peer")}
	request := Request{
		RAMMB: 128, VCPU: 4, CPUMillicores: 1000,
		PreferredNodeID: "cached", PreferredNodeIDs: []string{"cached"},
		restorePressureAware: true,
	}

	got, err := choosePlacementWithCPU(nodes, nil, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "cached" {
		t.Fatalf("low-pressure placement = %q, want cached for snapshot locality", got.NodeID)
	}

	request.restorePressureByNode = map[string]int{"cached": restorePressureHighWatermark, "peer": 0}
	got, err = choosePlacementWithCPU(nodes, nil, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "peer" {
		t.Fatalf("pressured placement = %q, want peer while cached node is at watermark", got.NodeID)
	}
}

func TestRestorePressurePlacementKeepsSmokeLocalityAndSingleFittingNode(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("cached"), cpuPlacementNode("peer")}
	request := Request{
		RAMMB: 128, VCPU: 4, CPUMillicores: 1000,
		PreferredNodeID: "cached", PreferredNodeIDs: []string{"cached"},
		PrioritizeSnapshotLocality: true,
		restorePressureAware:       true,
		restorePressureByNode:      map[string]int{"cached": restorePressureHighWatermark, "peer": 0},
	}
	got, err := choosePlacementWithCPU(nodes, nil, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "cached" {
		t.Fatalf("deployment-smoke placement = %q, want cached snapshot replica", got.NodeID)
	}

	// A fitting peer is required for pressure placement to change the result.
	usedMB := map[string]int64{"cached": 0, "peer": int64(nodes[1].AdmissionCeilingMB)}
	request.PrioritizeSnapshotLocality = false
	got, err = choosePlacementWithCPU(nodes, usedMB, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "cached" {
		t.Fatalf("single-fitting-node placement = %q, want cached", got.NodeID)
	}
}

func TestChooseRestorePlacementReservesAndReleasesInFlightCounts(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	e := newEngine(t, store, nil, nil, "").WithNodeRegistry(NewNodeRegistry([]state.ComputeNode{
		cpuPlacementNode("cached"), cpuPlacementNode("peer"),
	}))
	request := Request{
		RAMMB: 128, VCPU: 4, CPUMillicores: 1000,
		PreferredNodeID: "cached", PreferredNodeIDs: []string{"cached"},
	}

	first, releaseFirst, err := e.chooseRestorePlacementLocked(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := e.chooseRestorePlacementLocked(context.Background(), request)
	if err != nil {
		releaseFirst()
		t.Fatal(err)
	}
	third, releaseThird, err := e.chooseRestorePlacementLocked(context.Background(), request)
	if err != nil {
		releaseFirst()
		releaseSecond()
		t.Fatal(err)
	}
	if first.NodeID != "cached" || second.NodeID != "cached" {
		t.Fatalf("placements before watermark = %q, %q; want cached, cached", first.NodeID, second.NodeID)
	}
	if third.NodeID != "peer" {
		t.Fatalf("placement at watermark = %q, want peer", third.NodeID)
	}

	releaseFirst()
	releaseFirst() // a deferred cleanup after the explicit RPC release is safe
	releaseSecond()
	releaseThird()
	e.restorePlacementMu.Lock()
	defer e.restorePlacementMu.Unlock()
	if len(e.restorePlacementInFlight) != 0 {
		t.Fatalf("in-flight restore counts after release = %#v, want empty", e.restorePlacementInFlight)
	}
}
