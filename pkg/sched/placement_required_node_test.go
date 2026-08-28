// placement_required_node_test.go — ADR-119 v2 tests for the
// hard placement constraint (pkg/sched/admission.go::Request.RequiredNodeID,
// pkg/sched/placement.go::ChoosePlacement filter).
//
// Pins the v2 contract:
//
//   - RequiredNodeID set + matching active node + headroom →
//     returns the matching node (the wake lands on the IP's
//     owning compute_node).
//   - RequiredNodeID set + matching node is at capacity →
//     ErrCapacity (the wake refuses; the customer sees the
//     right wire shape rather than a silent source-spoof).
//   - RequiredNodeID set + no matching node → ErrCapacity
//     (the chooser filtered every candidate).
//   - RequiredNodeID empty → legacy least-loaded path (the
//     v1 single-box posture; no behaviour change for apps
//     without a static-egress pin).
//   - RequiredNodeID set + PreferredNodeID set to a
//     DIFFERENT node → RequiredNodeID wins (the hard
//     constraint beats the sticky-warm hint — a stale
//     sticky hint must never bypass the static-egress pin).
//
// These tests are pure-function (no Engine, no Ledger) so the
// contract reads as a one-shot table without the placement
// chooser's other dimensions polluting the picture.

package sched

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// requiredNodeFixture stands up a 3-node fleet: node-A
// (headroom), node-B (at usedB MB used), node-C (headroom).
// Used by the happy-path + capacity-saturated test cases.
func requiredNodeFixture(usedB int64) []state.ComputeNode {
	return []state.ComputeNode{
		node("node-A", "a", 0, 100),
		node("node-B", "b", usedB, 100),
		node("node-C", "c", 0, 100),
	}
}

// TestChoosePlacement_RequiredNodeID_HardConstraint picks
// node-A exactly when r.RequiredNodeID="node-A" — even though
// node-C has identical headroom. The point is that the
// chooser doesn't fall through to the tie-break.
func TestChoosePlacement_RequiredNodeID_HardConstraint(t *testing.T) {
	const req = 40 // 48 MB billable
	nodes := requiredNodeFixture(0)
	got, err := ChoosePlacement(nodes, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, Request{
		RAMMB: req, RequiredNodeID: "node-A",
	})
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID != "node-A" {
		t.Errorf("NodeID = %q, want node-A (RequiredNodeID must beat every other tie-break)", got.NodeID)
	}
}

// TestChoosePlacement_RequiredNodeID_SaturatedReturnsCapacity
// — the load-bearing branch. node-B is the only node that
// matches r.RequiredNodeID; node-B is at capacity (used_mb =
// ceiling → 0 free). The wake MUST refuse with ErrCapacity
// rather than fall through to node-A/node-C. Falling through
// would land the IP-pinned app on a non-owning node and
// source-spoof the egress at the switch (the v1 BYOIP
// impossibility ADR-119 fixed).
func TestChoosePlacement_RequiredNodeID_SaturatedReturnsCapacity(t *testing.T) {
	const req = 40 // 48 MB billable
	nodes := []state.ComputeNode{
		node("node-A", "a", 0, 100),   // 100 MB free
		node("node-B", "b", 100, 100), // 0 MB free (saturated)
		node("node-C", "c", 0, 100),   // 100 MB free
	}
	got, err := ChoosePlacement(nodes, map[string]int64{
		"node-A": 0, "node-B": 100, "node-C": 0,
	}, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, Request{
		RAMMB: req, RequiredNodeID: "node-B",
	})
	if err == nil {
		t.Fatalf("ChoosePlacement: want capacity err, got placement %+v", got)
	}
	var prob *api.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("err = %v, want *api.Problem", err)
	}
	if prob.Code != api.CodeCapacity {
		t.Errorf("problem code = %v, want CodeCapacity", prob.Code)
	}
}

// TestChoosePlacement_RequiredNodeID_NoMatchingNodeReturnsCapacity
// — RequiredNodeID references a node that doesn't exist in
// the fleet. The chooser filters every candidate and returns
// ErrCapacity.
func TestChoosePlacement_RequiredNodeID_NoMatchingNodeReturnsCapacity(t *testing.T) {
	const req = 40
	nodes := []state.ComputeNode{
		node("node-A", "a", 0, 100),
		node("node-C", "c", 0, 100),
	}
	got, err := ChoosePlacement(nodes, map[string]int64{
		"node-A": 0, "node-C": 0,
	}, map[string]int64{
		"node-A": 0, "node-C": 0,
	}, Request{
		RAMMB: req, RequiredNodeID: "node-Z", // not in fleet
	})
	if err == nil {
		t.Fatalf("ChoosePlacement: want capacity err, got placement %+v", got)
	}
	var prob *api.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("err = %v, want *api.Problem", err)
	}
	if prob.Code != api.CodeCapacity {
		t.Errorf("problem code = %v, want CodeCapacity", prob.Code)
	}
}

// TestChoosePlacement_RequiredNodeID_EmptyAllowsAny — the
// legacy v1 single-box posture. Empty RequiredNodeID falls
// through to the least-loaded tie-break; the only thing this
// test pins is "some candidate was returned" (the chooser
// must NOT refuse the wake just because the caller didn't
// set the v2 field).
func TestChoosePlacement_RequiredNodeID_EmptyAllowsAny(t *testing.T) {
	const req = 40
	nodes := requiredNodeFixture(0)
	got, err := ChoosePlacement(nodes, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, Request{
		RAMMB: req, RequiredNodeID: "", // empty
	})
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID == "" {
		t.Errorf("NodeID = empty, want one of node-A/B/C (empty RequiredNodeID must not refuse)")
	}
}

// TestChoosePlacement_RequiredNodeID_BeatsPreferredNodeID —
// defence-in-depth: a stale PreferredNodeID hint that points
// at a non-owning node MUST be ignored when RequiredNodeID is
// set. Otherwise a stale sticky-warm cache entry (e.g. an app
// that was warmed on node-C before its IP was pinned to
// node-A) would defeat the hard constraint.
func TestChoosePlacement_RequiredNodeID_BeatsPreferredNodeID(t *testing.T) {
	const req = 40
	nodes := requiredNodeFixture(0)
	got, err := ChoosePlacement(nodes, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, Request{
		RAMMB:           req,
		RequiredNodeID:  "node-A",
		PreferredNodeID: "node-C", // stale sticky hint
	})
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID != "node-A" {
		t.Errorf("NodeID = %q, want node-A (RequiredNodeID must beat PreferredNodeID)", got.NodeID)
	}
}

// TestChoosePlacement_RequiredNodeID_BeatsPreferredNodeIDs
// — same defence-in-depth, against the ready-replica set
// (issue #1054). The snapshot-replica bias must not deflect
// the chooser away from the IP's owning node when the ready
// replica set is on a different node.
func TestChoosePlacement_RequiredNodeID_BeatsPreferredNodeIDs(t *testing.T) {
	const req = 40
	nodes := requiredNodeFixture(0)
	got, err := ChoosePlacement(nodes, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, map[string]int64{
		"node-A": 0, "node-B": 0, "node-C": 0,
	}, Request{
		RAMMB:            req,
		RequiredNodeID:   "node-A",
		PreferredNodeIDs: []string{"node-C"},
	})
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if got.NodeID != "node-A" {
		t.Errorf("NodeID = %q, want node-A (RequiredNodeID must beat PreferredNodeIDs)", got.NodeID)
	}
}
