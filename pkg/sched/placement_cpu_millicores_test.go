// spec: §6.2
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func cpuPlacementNode(id string) state.ComputeNode {
	n := node(id, id, 0, 10_000)
	n.VPCPUs = 4
	n.VCPUBudget = 32
	return n
}

func TestChoosePlacementWithCPU_RejectsPhysicalOvercommit(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("a"), cpuPlacementNode("b")}
	got, err := choosePlacementWithCPU(
		nodes,
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 4000, "b": 1000},
		Request{RAMMB: 128, VCPU: 4, CPUMillicores: 1000},
	)
	if err != nil {
		t.Fatalf("choose placement: %v", err)
	}
	if got.NodeID != "b" {
		t.Fatalf("node = %q, want b (a has no physical CPU headroom)", got.NodeID)
	}
	if got.CPUBudgetMillicores != 4000 {
		t.Fatalf("CPU budget = %d, want 4000", got.CPUBudgetMillicores)
	}
}

func TestChoosePlacementWithCPU_CPUHeadroomOutranksWarmAffinity(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("a"), cpuPlacementNode("b")}
	got, err := choosePlacementWithCPU(
		nodes,
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 1000, "b": 0},
		Request{RAMMB: 128, VCPU: 4, CPUMillicores: 1000, PreferredNodeID: "a"},
	)
	if err != nil {
		t.Fatalf("choose placement: %v", err)
	}
	if got.NodeID != "b" {
		t.Fatalf("node = %q, want b (idle CPU outranks warm affinity)", got.NodeID)
	}
}

func TestChoosePlacementWithCPU_WarmAffinityWinsOnEqualCPUHeadroom(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("a"), cpuPlacementNode("b")}
	got, err := choosePlacementWithCPU(
		nodes,
		map[string]int64{"a": 1000, "b": 0},
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 0, "b": 0},
		Request{RAMMB: 128, VCPU: 4, CPUMillicores: 1000, PreferredNodeID: "a"},
	)
	if err != nil {
		t.Fatalf("choose placement: %v", err)
	}
	if got.NodeID != "a" {
		t.Fatalf("node = %q, want a (warm affinity is retained on equal CPU headroom)", got.NodeID)
	}
}

func TestChoosePlacementWithCPU_AllNodesAtPhysicalCapacity(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("a"), cpuPlacementNode("b")}
	_, err := choosePlacementWithCPU(
		nodes,
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 4000, "b": 4000},
		Request{RAMMB: 128, VCPU: 4, CPUMillicores: 1000},
	)
	if err == nil {
		t.Fatal("placement must fail when every node is at its physical CPU budget")
	}
}
