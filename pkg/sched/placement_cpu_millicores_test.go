// spec: §6.2
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cpuPlacementNode(id string) state.ComputeNode {
	n := node(id, id, 0, 10_000)
	// vpcpus is the PHYSICAL core count; the admission budget applies spec
	// §1's CPUOvercommit on top of it, matching the guest-vCPU budget below
	// (4 cores * 8 = 32 slots).
	n.VPCPUs = 4
	n.VCPUBudget = 32
	return n
}

// cpuSaturated is the used-millicores value that leaves a node with no CPU
// headroom. Derived from cpuBudgetMillicores so these tests track the one
// formula rather than restating a literal that silently drifts from it.
func cpuSaturated() int64 { return cpuBudgetMillicores(cpuPlacementNode("probe")) }

func TestChoosePlacementWithCPU_RejectsPhysicalOvercommit(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("a"), cpuPlacementNode("b")}
	got, err := choosePlacementWithCPU(
		nodes,
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": 0, "b": 0},
		map[string]int64{"a": cpuSaturated(), "b": 1000},
		Request{RAMMB: 128, VCPU: 4, CPUMillicores: 1000},
	)
	if err != nil {
		t.Fatalf("choose placement: %v", err)
	}
	if got.NodeID != "b" {
		t.Fatalf("node = %q, want b (a has no physical CPU headroom)", got.NodeID)
	}
	if want := int(cpuSaturated()); got.CPUBudgetMillicores != want {
		t.Fatalf("CPU budget = %d, want %d", got.CPUBudgetMillicores, want)
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
		map[string]int64{"a": cpuSaturated(), "b": cpuSaturated()},
		Request{RAMMB: 128, VCPU: 4, CPUMillicores: 1000},
	)
	if err == nil {
		t.Fatal("placement must fail when every node is at its physical CPU budget")
	}
}

// TestCPUBudgetMillicoresAppliesSpecOvercommit pins that the sustained-CPU
// budget uses spec §1's CPUOvercommit, and therefore agrees with the
// guest-vCPU budget about the same resource.
//
// An app's CPUMillicores is a cgroup v2 cpu.max quota — a burst ceiling, not
// a reservation — so summing those ceilings against raw physical cores
// reserves peak capacity for every idle instance. Before this, a 4-core node
// advertising vcpu_budget=32 admitted only 4 apps at the default 1000
// millicores, which made the overcommit-aware vCPU budget dead code and left
// production nodes unable to satisfy their own min_instances floors.
//
// adr: 204
// spec: §1
func TestCPUBudgetMillicoresAppliesSpecOvercommit(t *testing.T) {
	t.Parallel()
	n := cpuPlacementNode("a")
	want := int64(n.VPCPUs) * 1000 * int64(api.CPUOvercommit)
	if got := cpuBudgetMillicores(n); got != want {
		t.Fatalf("cpuBudgetMillicores = %d, want %d", got, want)
	}
	// The two gates must agree: a node whose guest-vCPU budget admits N
	// single-vCPU instances must also have the millicores headroom for N
	// instances at the default per-app quota.
	slots := int64(n.VCPUBudget)
	if got := cpuBudgetMillicores(n) / int64(api.DefaultAppCPUMillicores); got != slots {
		t.Fatalf("millicores budget funds %d default-quota instances, but vcpu_budget funds %d", got, slots)
	}
	// A node with no physical CPU count advertises no budget rather than an
	// unbounded one.
	n.VPCPUs = 0
	if got := cpuBudgetMillicores(n); got != 0 {
		t.Fatalf("cpuBudgetMillicores with vpcpus=0 = %d, want 0", got)
	}
}
