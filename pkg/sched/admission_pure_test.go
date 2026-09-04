// admission_pure_test.go — fill pkg/sched/admission.go coverage of
// the pure / no-store-required helper surface. Targets
// ConcurrencyForDeployment (0% in baseline) and the
// empty/non-empty/negative-clamp branches of HeadroomMB.
//
// Whitebox `package sched`.

package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// newTestLedger constructs a NodeLedger with the maps initialized
// so the helpers under test don't have to handle nil maps.
func newTestLedger() *NodeLedger {
	return &NodeLedger{
		resident:         map[string]*nodeReservation{},
		perApp:           map[string]int{},
		perAppDeployment: map[string]int{},
		entries:          map[string]*reservation{},
	}
}

func TestNodeLedger_ConcurrencyForDeployment_EmptyIDs(t *testing.T) {
	l := newTestLedger()
	if got := l.ConcurrencyForDeployment("", ""); got != 0 {
		t.Errorf("both empty: got %d, want 0", got)
	}
	if got := l.ConcurrencyForDeployment("app-1", ""); got != 0 {
		t.Errorf("empty dep: got %d, want 0", got)
	}
	if got := l.ConcurrencyForDeployment("", "dep-1"); got != 0 {
		t.Errorf("empty app: got %d, want 0", got)
	}
}

func TestNodeLedger_ConcurrencyForDeployment_Hit(t *testing.T) {
	l := newTestLedger()
	// Reach into the unexported field directly (whitebox).
	l.perAppDeployment["app-1\x00dep-1"] = 3
	if got := l.ConcurrencyForDeployment("app-1", "dep-1"); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestNodeLedger_ConcurrencyForDeployment_Miss(t *testing.T) {
	l := newTestLedger()
	if got := l.ConcurrencyForDeployment("app-1", "dep-1"); got != 0 {
		t.Errorf("miss: got %d, want 0", got)
	}
}

// --- HeadroomMB ----------------------------------------------------

func TestNodeLedger_HeadroomMB_EmptyReturnsCeiling(t *testing.T) {
	l := newTestLedger()
	// Empty resident map → back-compat branch returns the
	// global ceiling.
	if got := l.HeadroomMB(); got != api.RAMAdmissionCeilingMB {
		t.Errorf("empty: got %d, want %d", got, api.RAMAdmissionCeilingMB)
	}
}

func TestNodeLedger_HeadroomMB_NonEmptySumsNodeHeadroom(t *testing.T) {
	l := newTestLedger()
	// ceiling for one node, with 100 MB resident → head = ceiling - 100.
	const nodeID = "node-A"
	l.resident[nodeID] = &nodeReservation{residentRAM: 100, usedVCPU: 2}
	got := l.HeadroomMB()
	// Compute the expected ceiling via the unexported helper and
	// subtract the resident.
	want := l.ceilingForNode_locked(nodeID, api.Limits{}) - 100
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
	if got < 0 {
		t.Error("got negative headroom")
	}
}

// TestNodeLedger_ResidentFor pins the Task #62 source-ledger
// backstop surface: ResidentFor returns true after Admit, false
// after Release, and false on a nil receiver. The dead-node
// reconciler consults this to close the billing-leak race where
// a peer's failure path freed the row but not the ledger slot.
func TestNodeLedger_ResidentFor(t *testing.T) {
	l := newTestLedger()
	if l.ResidentFor("i-x") {
		t.Errorf("empty ledger: ResidentFor(i-x) = true; want false")
	}
	if err := l.Admit(Request{AppID: "app-1", DeploymentID: "dep-1", Instance: "i-x", RAMMB: 128, NodeID: "node-a", Plan: api.PlanHobby}); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if !l.ResidentFor("i-x") {
		t.Errorf("post-Admit: ResidentFor(i-x) = false; want true")
	}
	l.Release("i-x")
	if l.ResidentFor("i-x") {
		t.Errorf("post-Release: ResidentFor(i-x) = true; want false")
	}
	var nilL *NodeLedger
	if nilL.ResidentFor("any") {
		t.Errorf("nil receiver: ResidentFor = true; want false (bootstrap safety)")
	}
}

func TestNodeLedger_HeadroomMB_ClampsNegativeToZero(t *testing.T) {
	// Pin the defensive clamp at zero: the per-node view can go
	// negative if a Release races ahead of the resident accounting.
	// HeadroomMB clamps it.
	l := newTestLedger()
	l.resident["node-A"] = &nodeReservation{residentRAM: 1 << 30, usedVCPU: 1}
	got := l.HeadroomMB()
	if got != 0 {
		t.Errorf("got %d, want 0 (clamped)", got)
	}
}

func TestNodeLedger_HeadroomMB_UsesPerNodeCeilings(t *testing.T) {
	l := NewNodeLedger()
	for _, r := range []Request{
		{Instance: "a", AppID: "app-a", Plan: api.PlanFree, RAMMB: 128, VCPU: 1, MaxConcurrency: 1, NodeID: "small", NodeCeilingMB: 1000, VCPUBudget: api.VCPUSlots},
		{Instance: "b", AppID: "app-b", Plan: api.PlanFree, RAMMB: 128, VCPU: 1, MaxConcurrency: 1, NodeID: "large", NodeCeilingMB: 2000, VCPUBudget: api.VCPUSlots},
	} {
		if err := l.Admit(r); err != nil {
			t.Fatalf("Admit(%s): %v", r.Instance, err)
		}
	}
	// Each reservation consumes 128 MB + 8 MB overhead.
	const perInstance = 136
	want := (1000 - perInstance) + (2000 - perInstance)
	if got := l.HeadroomMB(); got != want {
		t.Fatalf("HeadroomMB = %d, want %d", got, want)
	}
}
