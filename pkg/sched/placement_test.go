// placement_test.go — table-driven tests for ChoosePlacement (issue #97
// / ADR-025 axis 3). The chooser is a pure function: every scenario
// here is a literal slice of ComputeNode + a map of used_mb, exactly
// what ActiveComputeNodes + ComputeNodeUsedMB return from production
// stores. No Postgres, no KVM, no goroutines.

package sched

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// node is a tiny constructor for test fixtures; keeps the table-driven
// cases readable (the alternative — full struct literals — is verbose
// for 5 scenarios). Sets Active=true and a default ceiling; individual
// cases override as needed.
func node(id, name string, usedMB int64, ceilingMB int) state.ComputeNode {
	return state.ComputeNode{
		ID: id, Name: name, TargetURL: "unix:///run/faas/" + name + ".sock",
		VPCPUs: 160, MemMB: ceilingMB + 8400, MaxConcurrency: 200,
		AdmissionCeilingMB: ceilingMB, Active: true,
	}
}

// TestChoosePlacement_Table drives the 5 scenarios documented in the PR
// plan §D. Each row is self-contained; the helper keeps the input shape
// (a slice of nodes + a map of used_mb) explicit at every call site so a
// future regression has one place to look.
func TestChoosePlacement_Table(t *testing.T) {
	const req = 40 // test request: 40 MB billable (rounds the overhead to 48 with +8)
	cases := []struct {
		name    string
		nodes   []state.ComputeNode
		usedMB  map[string]int64
		r       Request
		wantID  string // empty = expected error
		wantErr bool
	}{
		{
			name:   "least-loaded wins when only one fits",
			nodes:  []state.ComputeNode{node("a-id", "a", 0, 100), node("b-id", "b", 50, 100)},
			usedMB: map[string]int64{"a-id": 0, "b-id": 50},
			r:      Request{RAMMB: req},
			wantID: "a-id",
		},
		{
			name:   "lexicographic tie-break on equal headroom",
			nodes:  []state.ComputeNode{node("a-id", "a", 0, 100), node("b-id", "b", 0, 100)},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req},
			wantID: "a-id", // 'a' < 'b'
		},
		{
			name:   "least-loaded wins across multiple nodes with capacity",
			nodes:  []state.ComputeNode{node("a-id", "a", 80, 100), node("b-id", "b", 0, 100)},
			usedMB: map[string]int64{"a-id": 80, "b-id": 0},
			r:      Request{RAMMB: req},
			wantID: "b-id", // b has 100 free vs a's 20; b fits, a doesn't (80+48=128 > 100)
		},
		{
			name:    "no node fits returns capacity error",
			nodes:   []state.ComputeNode{node("a-id", "a", 80, 100), node("b-id", "b", 70, 100)},
			usedMB:  map[string]int64{"a-id": 80, "b-id": 70},
			r:       Request{RAMMB: req}, // 48 billable; neither 80+48 nor 70+48 fits in 100
			wantErr: true,
		},
		{
			name:    "inactive nodes are skipped",
			nodes:   []state.ComputeNode{{ID: "a-id", Name: "a", TargetURL: "unix:///x", AdmissionCeilingMB: 100, Active: false}},
			usedMB:  map[string]int64{"a-id": 0},
			r:       Request{RAMMB: req},
			wantErr: true,
		},
		{
			name:   "single-node fleet degenerates to that node (default-local case)",
			nodes:  []state.ComputeNode{node("local-id", "default-local", 1000, api.RAMAdmissionCeilingMB)},
			usedMB: map[string]int64{"local-id": 1000},
			r:      Request{RAMMB: 512},
			wantID: "local-id",
		},
		{
			name:   "missing usedMB key treated as 0",
			nodes:  []state.ComputeNode{node("a-id", "a", 0, 100)},
			usedMB: map[string]int64{}, // a-id absent
			r:      Request{RAMMB: req},
			wantID: "a-id",
		},
		{
			name:    "nodes with zero ceiling are skipped (defensive)",
			nodes:   []state.ComputeNode{{ID: "a-id", Name: "a", TargetURL: "unix:///x", AdmissionCeilingMB: 0, Active: true}},
			usedMB:  map[string]int64{},
			r:       Request{RAMMB: req},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChoosePlacement(tc.nodes, tc.usedMB, tc.r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got placement %+v", got)
				}
				var prob *api.Problem
				if !errors.As(err, &prob) || prob.Code != api.CodeCapacity {
					t.Errorf("expected capacity problem, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.NodeID != tc.wantID {
				t.Errorf("NodeID = %q, want %q (placement = %+v)", got.NodeID, tc.wantID, got)
			}
			if got.TargetURL == "" {
				t.Errorf("TargetURL empty on chosen placement (chose %+v) — caller has no dial target", got)
			}
			if got.CeilingMB <= 0 {
				t.Errorf("CeilingMB = %d, want > 0", got.CeilingMB)
			}
		})
	}
}

// TestChoosePlacement_RejectsNonPositiveRAM pins the "RAM must be
// positive" guard. A zero-RAM request would silently land on the first
// active node with zero check, which is wrong (every real instance
// carries at least the +8 MB overhead).
func TestChoosePlacement_RejectsNonPositiveRAM(t *testing.T) {
	nodes := []state.ComputeNode{node("a-id", "a", 0, 1000)}
	usedMB := map[string]int64{"a-id": 0}
	_, err := ChoosePlacement(nodes, usedMB, Request{RAMMB: 0})
	if err == nil {
		t.Fatal("expected error for zero RAM")
	}
	_, err = ChoosePlacement(nodes, usedMB, Request{RAMMB: -10})
	if err == nil {
		t.Fatal("expected error for negative RAM")
	}
}

// TestChoosePlacement_BillableIncludesOverhead pins the "+8 MB overhead
// is part of the per-node headroom check" contract. A request of
// ram_mb=92 on a node with 100 MB ceiling and 0 used has billable =
// 100 (92 + 8); a request of ram_mb=92 on a node with 0 used and
// ceiling=99 is refused (100 > 99). The overhead is the same number
// the ledger charges per live instance (spec §4.7), so the placement
// decision and the post-admit ledger can't disagree.
func TestChoosePlacement_BillableIncludesOverhead(t *testing.T) {
	ceilingNode := state.ComputeNode{
		ID: "tight", Name: "tight", TargetURL: "unix:///tight.sock",
		VPCPUs: 160, MemMB: 999, MaxConcurrency: 200,
		AdmissionCeilingMB: 100, Active: true,
	}
	r := Request{RAMMB: 92} // billable = 100
	usedMB := map[string]int64{"tight": 0}

	if _, err := ChoosePlacement([]state.ComputeNode{ceilingNode}, usedMB, r); err != nil {
		t.Errorf("100 MB ceiling should fit 100 MB billable, got %v", err)
	}
	ceilingNode.AdmissionCeilingMB = 99 // 100 > 99 → no fit
	if _, err := ChoosePlacement([]state.ComputeNode{ceilingNode}, usedMB, r); err == nil {
		t.Error("99 MB ceiling must refuse 100 MB billable (overhead included)")
	}
}
