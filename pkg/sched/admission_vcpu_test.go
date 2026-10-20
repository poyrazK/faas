// admission_vcpu_test.go — Tier A2 per-node vCPU budget tests at
// the ledger layer.
//
// Pins the load-bearing enforcement:
//   - the per-node vCPU budget replaces the box-wide
//     api.VCPUSlots gate inside Admit;
//   - a fleet of N nodes with vcpu_budget=160 each can admit
//     160 × N total vCPU (not capped at 160 globally);
//   - Request.VCPUBudget=0 falls back to api.VCPUSlots (the
//     safe default for un-registered nodes and pre-multi-node
//     test seams);
//   - the vCPU headroom and RAM headroom checks are independent:
//     a node at RAM cap but with vCPU headroom rejects on RAM
//     only, and vice versa.

package sched_test

import (
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// TestAdmit_PerNodeVCPUEnforced pins the load-bearing invariant:
// a fleet of two nodes with vcpu_budget=160 each can collectively
// admit 320 vCPU (not capped at the legacy box-wide 160). The
// pre-Tier-A2 ledger would have rejected the second admit on the
// 161st vCPU; with Tier A2 each node enforces its own 160.
func TestAdmit_PerNodeVCPUEnforced(t *testing.T) {
	t.Parallel()
	l := sched.NewNodeLedger()

	// Fill node A to its 160 vCPU budget with single-vCPU admits.
	// Distinct appIDs avoid the per-app concurrency gate.
	for i := 0; i < 160; i++ {
		err := l.Admit(sched.Request{
			Instance: "a-" + strconv.Itoa(i),
			AppID:    "a-app-" + strconv.Itoa(i),
			Plan:     api.PlanHobby,
			RAMMB:    256, VCPU: 1, MaxConcurrency: 1,
			NodeID: "node-a", NodeCeilingMB: 100000, VCPUBudget: 160,
		})
		if err != nil {
			t.Fatalf("filler %d: %v", i, err)
		}
	}
	// Node B is empty; an admit with VCPU=1 fits.
	if err := l.Admit(sched.Request{
		Instance: "b-1", AppID: "b-app-1",
		Plan: api.PlanHobby, RAMMB: 256, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-b", NodeCeilingMB: 100000, VCPUBudget: 160,
	}); err != nil {
		t.Fatalf("node b admit after node a saturated: %v", err)
	}
	// A second admit on node B fills it. A third is rejected.
	if err := l.Admit(sched.Request{
		Instance: "b-160", AppID: "b-app-160",
		Plan: api.PlanHobby, RAMMB: 256, VCPU: 159, MaxConcurrency: 1,
		NodeID: "node-b", NodeCeilingMB: 100000, VCPUBudget: 160,
	}); err != nil {
		t.Fatalf("node b second admit: %v", err)
	}
	// Third admit pushes B over 160 vCPU.
	err := l.Admit(sched.Request{
		Instance: "b-over", AppID: "b-app-over",
		Plan: api.PlanHobby, RAMMB: 256, VCPU: 1, MaxConcurrency: 1,
		NodeID: "node-b", NodeCeilingMB: 100000, VCPUBudget: 160,
	})
	if err == nil {
		t.Fatal("node b over vCPU budget must be rejected")
	}
}

// TestAdmit_VCPUBudgetZeroFallback pins the legacy single-box
// safety net: Request.VCPUBudget=0 falls back to
// api.VCPUSlots (160). A pre-Tier-A2 test seam that doesn't
// thread the budget through continues to work, capped at 160
// per node (the legacy posture).
func TestAdmit_VCPUBudgetZeroFallback(t *testing.T) {
	t.Parallel()
	l := sched.NewNodeLedger()
	// Fill to 160 with VCPUBudget=0 (legacy / un-registered node).
	for i := 0; i < 160; i++ {
		err := l.Admit(sched.Request{
			Instance: "f-" + strconv.Itoa(i),
			AppID:    "f-app-" + strconv.Itoa(i),
			Plan:     api.PlanHobby,
			RAMMB:    128, VCPU: 1, MaxConcurrency: 1,
			NodeID: "legacy-node", NodeCeilingMB: 100000,
			// VCPUBudget deliberately omitted (zero value)
		})
		if err != nil {
			t.Fatalf("filler %d: %v (VCPUBudget=0 must fall back to api.VCPUSlots)", i, err)
		}
	}
	// 161st admit on the same node must be rejected.
	err := l.Admit(sched.Request{
		Instance: "over", AppID: "over-app",
		Plan: api.PlanHobby, RAMMB: 128, VCPU: 1, MaxConcurrency: 1,
		NodeID: "legacy-node", NodeCeilingMB: 100000,
	})
	if err == nil {
		t.Fatal("legacy node over api.VCPUSlots must be rejected")
	}
}

// TestAdmit_RAMAndVCPUIndependent pins the two checks as
// independent gates. A node has small RAM (256 MB ceiling) and
// ample vCPU budget. A request that fits vCPU but exceeds RAM
// is rejected on RAM. The reverse is also true: ample RAM but
// vCPU at budget → rejected on vCPU.
func TestAdmit_RAMAndVCPUIndependent(t *testing.T) {
	t.Parallel()
	l := sched.NewNodeLedger()

	// Saturate RAM only: 128 MB request × 2 = 256 + 8 + 8 = 272 MB → 264 > 256 ceiling
	if err := l.Admit(sched.Request{
		Instance: "r1", AppID: "ram-app-1",
		Plan: api.PlanHobby, RAMMB: 128, VCPU: 1, MaxConcurrency: 1,
		NodeID: "mixed", NodeCeilingMB: 256, VCPUBudget: 160,
	}); err != nil {
		t.Fatalf("first ram admit: %v", err)
	}
	// Second 128 MB on this node should be rejected on RAM (264 > 256).
	err := l.Admit(sched.Request{
		Instance: "r2", AppID: "ram-app-2",
		Plan: api.PlanHobby, RAMMB: 128, VCPU: 1, MaxConcurrency: 1,
		NodeID: "mixed", NodeCeilingMB: 256, VCPUBudget: 160,
	})
	if err == nil {
		t.Fatal("RAM headroom exceeded must be rejected (vCPU has plenty of headroom)")
	}

	// Fresh ledger; saturate vCPU only: budget=4, VCPU=3 → 1 admit
	// (used=3, fits), 2nd admit (used=3+3=6 > 4) rejected on vCPU.
	l2 := sched.NewNodeLedger()
	if err := l2.Admit(sched.Request{
		Instance: "v1", AppID: "vcpu-app-1",
		Plan: api.PlanHobby, RAMMB: 128, VCPU: 3, MaxConcurrency: 1,
		NodeID: "vnode", NodeCeilingMB: 100000, VCPUBudget: 4,
	}); err != nil {
		t.Fatalf("first vcpu admit: %v", err)
	}
	err = l2.Admit(sched.Request{
		Instance: "v2", AppID: "vcpu-app-2",
		Plan: api.PlanHobby, RAMMB: 128, VCPU: 3, MaxConcurrency: 1,
		NodeID: "vnode", NodeCeilingMB: 100000, VCPUBudget: 4,
	})
	if err == nil {
		t.Fatal("vCPU headroom exceeded must be rejected (RAM has plenty of headroom)")
	}
}
