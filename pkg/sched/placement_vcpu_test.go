// placement_vcpu_test.go — Tier A2 per-node vCPU budget tests.
//
// Pins the vCPU fit check + the vCPU headroom tie-break:
//   - a node with vcpu_budget smaller than the request's vCPU
//     is rejected;
//   - a node at budget headroom equal to a competitor wins on
//     RAM headroom primary, vCPU headroom secondary;
//   - a node with vcpu_budget=0 is excluded (defensive — the
//     migration default is 160, but a freshly-INSERTed row before
//     the DEFAULT backfilled could in theory hit zero);
//   - the legacy box-wide api.VCPUSlots gate is replaced: a fleet
//     of N nodes with smaller per-node budgets cannot collectively
//     admit more than Σ(vcpu_budget).

package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestChoosePlacement_RejectsOverVCPUBudget pins the per-node
// vCPU fit check. The request is 4 vCPU; node a has budget=2
// (a tiny test-only value), node b has budget=160. Only b fits.
func TestChoosePlacement_RejectsOverVCPUBudget(t *testing.T) {
	t.Parallel()
	a := sched.Node("a-id", "a", 0, 1000)
	a.VCPUBudget = 2 // request is 4 vCPU → a is rejected
	b := sched.Node("b-id", "b", 0, 1000)
	b.VCPUBudget = 160
	nodes := []state.ComputeNode{a, b}
	usedMB := map[string]int64{"a-id": 0, "b-id": 0}
	usedVCPU := map[string]int64{"a-id": 0, "b-id": 0}
	r := sched.Request{RAMMB: 64, VCPU: 4}

	got, err := sched.ChoosePlacement(nodes, usedMB, usedVCPU, r)
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID != "b-id" {
		t.Errorf("NodeID = %q, want b-id (a is over vCPU budget)", got.NodeID)
	}
}

// TestChoosePlacement_VCPUBudgetZeroExcluded pins the defensive
// "no budget" path: a node with vcpu_budget=0 is skipped, even
// if it has RAM headroom. This protects against an operator that
// forgets to set the budget, or a row that was inserted before
// migration 00123 ran (the DEFAULT 160 should backfill, but a
// test or a manual psql that bypasses the default is a real
// risk).
func TestChoosePlacement_VCPUBudgetZeroExcluded(t *testing.T) {
	t.Parallel()
	a := sched.Node("a-id", "a", 0, 1000)
	a.VCPUBudget = 0 // defensive exclusion
	nodes := []state.ComputeNode{a}
	usedMB := map[string]int64{"a-id": 0}
	usedVCPU := map[string]int64{"a-id": 0}
	r := sched.Request{RAMMB: 64, VCPU: 1}

	if _, err := sched.ChoosePlacement(nodes, usedMB, usedVCPU, r); err == nil {
		t.Error("node with vcpu_budget=0 must be excluded from the candidate set")
	}
}

// TestBetterCandidate_VCPUHeadroomTieBreaks pins the secondary
// vCPU headroom tie-break. Two nodes have equal RAM headroom;
// node a's vCPU budget is fully consumed (usedVCPU matches
// budget) and node b has vCPU headroom. Tied on RAM → b wins
// on vCPU.
func TestBetterCandidate_VCPUHeadroomTieBreaks(t *testing.T) {
	t.Parallel()
	a := sched.Node("a-id", "a", 0, 100)
	a.VCPUBudget = 4
	b := sched.Node("b-id", "b", 0, 100)
	b.VCPUBudget = 16
	usedMB := map[string]int64{"a-id": 0, "b-id": 0}
	usedVCPU := map[string]int64{"a-id": 4, "b-id": 0} // a is at vCPU cap
	nodes := []state.ComputeNode{a, b}
	r := sched.Request{RAMMB: 32, VCPU: 2}

	got, err := sched.ChoosePlacement(nodes, usedMB, usedVCPU, r)
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID != "b-id" {
		t.Errorf("NodeID = %q, want b-id (a at vCPU cap, b has headroom; equal RAM)", got.NodeID)
	}
}

// TestChoosePlacement_LegacySingleBoxUnchanged pins the backwards-
// compat posture. A single-node fleet with vcpu_budget=160 (the
// migration default + the memstore seed) and a request that fits
// in 160 vCPU and 47600 MB succeeds. This is the pre-Tier-A2
// single-box path preserved bit-for-bit.
func TestChoosePlacement_LegacySingleBoxUnchanged(t *testing.T) {
	t.Parallel()
	a := sched.Node("local-id", state.DefaultLocalNodeName, 0, api.RAMAdmissionCeilingMB)
	a.VCPUBudget = api.VCPUSlots // 160, the migration default
	nodes := []state.ComputeNode{a}
	usedMB := map[string]int64{"local-id": 0}
	usedVCPU := map[string]int64{"local-id": 0}
	r := sched.Request{RAMMB: 512, VCPU: 2}

	got, err := sched.ChoosePlacement(nodes, usedMB, usedVCPU, r)
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID != "local-id" {
		t.Errorf("NodeID = %q, want local-id (single-box)", got.NodeID)
	}
	if got.VCPUBudget != api.VCPUSlots {
		t.Errorf("Placement.VCPUBudget = %d, want %d (engine threads the row value through)", got.VCPUBudget, api.VCPUSlots)
	}
}
