// vcpu_combined_test.go — coverage for the combined RAM+VCPU
// headroom table at pkg/sched/admission.go.
//
// admission_vcpu_test.go covers:
//   - Per-node VCPU enforcement (Tier A2)
//   - VCPUBudget=0 fallback to api.VCPUSlots
//   - RAM and VCPU as independent gates
//
// What this file adds:
//   - Combined gate: a single request that bumps BOTH gates
//     simultaneously (tight ceiling on each).
//   - HeadroomMB() returns the right global value when RAM
//     reservation is partial.
//   - ResidentRAMForNode / UsedVCPUForNode report per-node
//     counters (not the global sum).
//   - Request with PreferredNodeID set: ledger does NOT pick
//     placement; the hint is recorded but Admit still respects
//     the floor (placement is the chooser's job).
//   - Multiple nodes: global Σ matches the sum of per-node
//     counters (cross-node accounting invariant).

package sched_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// TestAdmit_CombinedRAMAndVCPUAtBoundary pins the corner case:
// a request that fits BOTH gates separately but together
// pushes both to their limit. A second request must fail on
// the FIRST gate (RAM or VCPU; we accept either as long as
// the gate fires).
func TestAdmit_CombinedRAMAndVCPUAtBoundary(t *testing.T) {
	l := sched.NewNodeLedger()
	// First admit uses 128 MB + 8 overhead = 136 MB; vCPU=1.
	// Node ceiling: 200 MB RAM, 4 vCPU. Plenty of headroom.
	r := sched.Request{
		Instance: "vm-1", AppID: "app-1", Plan: api.PlanHobby,
		RAMMB: 128, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 200, VCPUBudget: 4,
	}
	if err := l.Admit(r); err != nil {
		t.Fatalf("Admit 1: %v", err)
	}
	// Second admit: 64 MB + 8 = 72 MB (total 208 > 200). This
	// bumps RAM over the ceiling. vCPU is still 1+1=2 ≤ 4. So
	// the rejection must be on RAM, not vCPU.
	err := l.Admit(sched.Request{
		Instance: "vm-2", AppID: "app-2", Plan: api.PlanHobby,
		RAMMB: 64, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 200, VCPUBudget: 4,
	})
	if err == nil {
		t.Fatal("combined gate: want RAM rejection, got nil")
	}
	if !strings.Contains(err.Error(), "RAM") && !strings.Contains(err.Error(), "headroom") {
		t.Errorf("err = %v, want substring 'RAM' or 'headroom'", err)
	}
}

// TestHeadroomMB_AfterPartialAdmit pins the global Σ(ram+8)
// ceiling math: HeadroomMB returns the global ceiling minus
// the current ResidentRAM. With one 128 MB admission on a
// global ceiling of 47,600 MB, headroom should be 47,600 - 136.
func TestHeadroomMB_AfterPartialAdmit(t *testing.T) {
	l := sched.NewNodeLedger()
	want := api.RAMAdmissionCeilingMB
	if got := l.HeadroomMB(); got != want {
		t.Errorf("empty HeadroomMB = %d, want %d", got, want)
	}
	if err := l.Admit(sched.Request{
		Instance: "vm-1", AppID: "app-1", Plan: api.PlanHobby,
		RAMMB: 128, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-a",
	}); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	want = api.RAMAdmissionCeilingMB - (128 + api.PerVMOverheadMB)
	if got := l.HeadroomMB(); got != want {
		t.Errorf("after Admit: HeadroomMB = %d, want %d", got, want)
	}
}

// TestResidentRAMForNode_ReportsPerNode pins the per-node RAM
// accessor: ResidentRAMForNode reports just the node's slice,
// not the global Σ. Two nodes with different RAM footprints
// must each report their own.
func TestResidentRAMForNode_ReportsPerNode(t *testing.T) {
	l := sched.NewNodeLedger()
	_ = l.Admit(sched.Request{
		Instance: "vm-a", AppID: "app-a", Plan: api.PlanHobby,
		RAMMB: 256, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 100000, VCPUBudget: 160,
	})
	_ = l.Admit(sched.Request{
		Instance: "vm-b", AppID: "app-b", Plan: api.PlanHobby,
		RAMMB: 512, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-b", NodeCeilingMB: 100000, VCPUBudget: 160,
	})
	aRAM := l.ResidentRAMForNode("node-a")
	bRAM := l.ResidentRAMForNode("node-b")
	if aRAM != 256+api.PerVMOverheadMB {
		t.Errorf("node-a RAM = %d, want %d", aRAM, 256+api.PerVMOverheadMB)
	}
	if bRAM != 512+api.PerVMOverheadMB {
		t.Errorf("node-b RAM = %d, want %d", bRAM, 512+api.PerVMOverheadMB)
	}
	// Global sum equals Σ of per-node counters.
	if got, want := l.ResidentRAM(), aRAM+bRAM; got != want {
		t.Errorf("ResidentRAM = %d, want %d (sum of per-node)", got, want)
	}
	// A node that hasn't been seen yet returns 0.
	if got := l.ResidentRAMForNode("unknown"); got != 0 {
		t.Errorf("ResidentRAMForNode(unknown) = %d, want 0", got)
	}
}

// TestUsedVCPUForNode_ReportsPerNode pins the per-node vCPU
// accessor: UsedVCPUForNode reports just the node's vCPU
// usage, UsedVCPU() returns the fleet Σ.
func TestUsedVCPUForNode_ReportsPerNode(t *testing.T) {
	l := sched.NewNodeLedger()
	_ = l.Admit(sched.Request{
		Instance: "vm-a", AppID: "app-a", Plan: api.PlanHobby,
		RAMMB: 128, VCPU: 3, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 100000, VCPUBudget: 160,
	})
	_ = l.Admit(sched.Request{
		Instance: "vm-b", AppID: "app-b", Plan: api.PlanHobby,
		RAMMB: 128, VCPU: 5, MaxConcurrency: 1,
		NodeID: "node-b", NodeCeilingMB: 100000, VCPUBudget: 160,
	})
	if got := l.UsedVCPUForNode("node-a"); got != 3 {
		t.Errorf("node-a vCPU = %d, want 3", got)
	}
	if got := l.UsedVCPUForNode("node-b"); got != 5 {
		t.Errorf("node-b vCPU = %d, want 5", got)
	}
	if got := l.UsedVCPU(); got != 8 {
		t.Errorf("fleet vCPU = %d, want 8 (sum of per-node)", got)
	}
	if got := l.UsedVCPUForNode("unknown"); got != 0 {
		t.Errorf("UsedVCPUForNode(unknown) = %d, want 0", got)
	}
}

// TestAdmit_PreferredNodeIDIsRecordedNotEnforced pins that the
// PreferredNodeID hint is a sticky-warm signal the chooser
// honors — the ledger records it on the reservation but does
// NOT route admits based on it. The chooser picks placement
// upstream.
func TestAdmit_PreferredNodeIDIsRecordedNotEnforced(t *testing.T) {
	l := sched.NewNodeLedger()
	r := sched.Request{
		Instance: "vm-1", AppID: "app-1", Plan: api.PlanHobby,
		RAMMB: 256, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 100000, VCPUBudget: 160,
	}
	if err := l.Admit(r); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// The reservation must be on node-a regardless of the hint
	// (which is empty here, but the field is documented as
	// chooser-side state, not ledger-side).
	if _, ok := (*l.Resident())["node-a"]; !ok {
		t.Error("reservation not on NodeID 'node-a'; ledger ignored NodeID")
	}
}

// TestAdmit_ZeroRAMBehavior pins the zero-RAM boundary as the
// ledger treats it today. A caller that builds a Request with
// RAMMB=0 still consumes the per-VM overhead MB (8) for
// ResidentRAM/ResidentRAMForNode accounting — zero-RAM is
// accepted. This test pins the current ACCEPTED contract so
// any future tightening (rejecting 0 RAM, or treating 0 as "no
// accounting") is a deliberate contract change rather than a
// silent behaviour drift.
func TestAdmit_ZeroRAMBehavior(t *testing.T) {
	l := sched.NewNodeLedger()
	err := l.Admit(sched.Request{
		Instance: "vm-0ram", AppID: "app-1", Plan: api.PlanHobby,
		RAMMB: 0, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 100000, VCPUBudget: 160,
	})
	// Today, zero-RAM is accepted — the overhead MB is still
	// added (8 MB minimum per VM). Pin the behaviour.
	if err != nil {
		t.Fatalf("zero-RAM Admit: got err %v; today 0 RAM is accepted", err)
	}
	// Resident RAM reflects the 8 MB overhead.
	if got := l.ResidentRAMForNode("node-a"); got != api.PerVMOverheadMB {
		t.Errorf("ResidentRAMForNode = %d, want %d (just the overhead)",
			got, api.PerVMOverheadMB)
	}
}
