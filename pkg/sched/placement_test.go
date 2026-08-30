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

// strPtr is a tiny helper for table rows that need region/zone
// pointers (placement.go tie-break orders on these; nil must sort the
// same as "" so pre-00069 rows don't bias the result).
func strPtr(s string) *string { return &s }

// node is a tiny constructor for test fixtures; keeps the table-driven
// cases readable (the alternative — full struct literals — is verbose
// for 5 scenarios). Sets Active=true and a default ceiling; individual
// cases override as needed. Region/Zone are nil so callers opt in
// explicitly (pre-00069 shape). VCPUBudget is seeded to 160
// (api.VCPUSlots) so the Tier A2 vCPU fit check accepts the
// request; the vCPU-headroom-specific scenarios override it
// explicitly.
func node(id, name string, usedMB int64, ceilingMB int) state.ComputeNode {
	return state.ComputeNode{
		ID: id, Name: name, TargetURL: "unix:///run/faas/" + name + ".sock",
		VPCPUs: 160, MemMB: ceilingMB + 8400, MaxConcurrency: 200,
		AdmissionCeilingMB: ceilingMB, VCPUBudget: 160,
		Lifecycle: state.NodeLifecycleActive, Active: true,
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
			name: "inactive nodes are skipped",
			nodes: []state.ComputeNode{{
				ID: "a-id", Name: "a", TargetURL: "unix:///x",
				AdmissionCeilingMB: 100,
				Lifecycle:          state.NodeLifecycleUnavailable, Active: false,
			}},
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
		{
			// Sticky-warm hint honored when the preferred node has
			// headroom (ADR-009 snapshot/page-cache benefit). Even
			// though b has strictly more free RAM, the hint pins a.
			name: "sticky-warm hint honored when preferred has headroom",
			nodes: []state.ComputeNode{
				node("a-id", "a", 30, 100), // a: 70 headroom, hint target
				node("b-id", "b", 0, 100),  // b: 100 headroom, least-loaded
			},
			usedMB: map[string]int64{"a-id": 30, "b-id": 0},
			r:      Request{RAMMB: req, PreferredNodeID: "a-id"},
			wantID: "a-id",
		},
		{
			// Affinity is bias, not a gate: when the hint target is
			// saturated, fall through to the least-loaded node. This
			// preserves the headroom invariant (ADR-005: cold-boot
			// always works; the chooser must not place into a node
			// that can't fit the request).
			name: "sticky-warm hint ignored when preferred is saturated",
			nodes: []state.ComputeNode{
				node("a-id", "a", 90, 100), // a: 10 headroom, hint target, but 10+48=58 > 10 → no fit
				node("b-id", "b", 0, 100),  // b: 100 headroom, least-loaded
			},
			usedMB: map[string]int64{"a-id": 90, "b-id": 0},
			r:      Request{RAMMB: req, PreferredNodeID: "a-id"},
			wantID: "b-id",
		},
		{
			// Hint targets a node that doesn't exist (or was
			// de-registered since the hint was set). Falls through
			// to least-loaded — silently choosing a non-existent
			// node would be worse than ignoring the hint.
			name: "sticky-warm hint for missing node falls through",
			nodes: []state.ComputeNode{
				node("a-id", "a", 80, 100), // a: 20 headroom, does not fit 48
				node("b-id", "b", 0, 100),  // b: 100 headroom
			},
			usedMB: map[string]int64{"a-id": 80, "b-id": 0},
			r:      Request{RAMMB: req, PreferredNodeID: "ghost-id"},
			wantID: "b-id",
		},
		{
			// Cold-boot path (ADR-005): no hint → least-loaded wins.
			// This is the single-box install case before any
			// WarmAffinity.RecordWake call has landed.
			name: "cold-boot path with no hint returns least-loaded",
			nodes: []state.ComputeNode{
				node("a-id", "a", 80, 100), // a: 20 headroom, does not fit 48
				node("b-id", "b", 0, 100),
			},
			usedMB: map[string]int64{"a-id": 80, "b-id": 0},
			r:      Request{RAMMB: req}, // no PreferredNodeID
			wantID: "b-id",
		},
		{
			// Per-row ceiling respected: nodes[].AdmissionCeilingMB is
			// the per-node ceiling (not the global RAMAdmissionCeilingMB).
			// Here node a's ceiling is 80 (smaller than b's 100), and the
			// request is 50 billable. Both have zero used, but a fits
			// exactly (0+50=50 ≤ 80) while b is preferred on headroom.
			// Tie-break on (region, name): b wins on headroom 100 vs 80.
			name: "per-row ceiling respected (ceiling < RAMAdmissionCeilingMB)",
			nodes: []state.ComputeNode{
				node("a-id", "a", 0, 80),
				node("b-id", "b", 0, 100),
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: 50},
			wantID: "b-id", // 100 headroom > 80 headroom
		},
		{
			// Per-row ceiling refuses a request the global ceiling
			// would have allowed. a's ceiling is 80; billable is 90.
			// The global ceiling (api.RAMAdmissionCeilingMB = 47600)
			// would have allowed this, but the per-row ceiling is
			// the source of truth — operators set per-row ceilings
			// to fence smaller boxes inside a heterogeneous fleet.
			name: "per-row ceiling refuses over-ceiling request",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "a", TargetURL: "unix:///a", AdmissionCeilingMB: 80, Active: true},
				node("b-id", "b", 0, 100),
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: 90}, // 98 billable; a refuses (98 > 80)
			wantID: "b-id",
		},
		{
			// Tie-break on (region, name) when headroom is equal. a
			// and b both have 100 headroom; tie-break sorts by region
			// ascending — a is in "eu-fra", b is in "us-east". The
			// chooser must prefer a.
			name: "tie-break on region when headroom equal",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "a", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra"), Zone: strPtr("eu-fra-1")},
				{ID: "b-id", Name: "b", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east"), Zone: strPtr("us-east-1")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req},
			wantID: "a-id",
		},
		{
			// Tie-break on zone when region is equal. Both in
			// "eu-fra"; zone a-1 sorts before zone a-2.
			name: "tie-break on zone when region equal",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "a", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra"), Zone: strPtr("eu-fra-2")},
				{ID: "b-id", Name: "b", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra"), Zone: strPtr("eu-fra-1")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req},
			wantID: "b-id",
		},
		{
			// Pre-00069 rows have nil region/zone. nil and "" must
			// sort identically so a fleet mid-rollout doesn't bias
			// toward the post-migration rows.
			name: "nil region sorts as empty string",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "a", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true}, // nil region
				{ID: "b-id", Name: "b", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("z-region")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req},
			wantID: "a-id", // "" < "z-region"
		},
		{
			// Sticky-warm hint AND equal headroom tie-break:
			// least-loaded path (hint missing) still respects
			// region/name ordering. a and b both fit with equal
			// headroom; b's region "us-east" sorts after a's
			// "eu-fra", so a wins even though b is alphabetically
			// later.
			name: "region tie-break beats name tie-break",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "aardvark", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra"), Zone: strPtr("eu-fra-1")},
				{ID: "b-id", Name: "bison", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east"), Zone: strPtr("us-east-1")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req},
			wantID: "a-id",
		},
		// ADR-098 PR-D: connection-aware upstream fit.
		// The four scenarios below pin the upstream_fit
		// tie-break ordering. The load-bearing claim is:
		// PreferredRegion == "" MUST fall through to the
		// legacy tie-break (fail-open); PreferredRegion != ""
		// MUST prefer the candidate whose compute_node.region
		// matches.
		{
			// Scenario A: nil scores → legacy. Both nodes
			// fit with equal headroom; PreferredRegion="".
			// The legacy region ASC tie-break wins (eu-fra
			// sorts before us-east).
			name: "nil preferred region fails open to legacy",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "aardvark", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra")},
				{ID: "b-id", Name: "bison", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req, PreferredRegion: ""}, // upstream affinity cache cold
			wantID: "a-id",                                   // legacy: "eu-fra" < "us-east"
		},
		{
			// Scenario B: bias wins above delta. Both nodes
			// fit with equal headroom + vCPU; PreferredRegion
			// = "us-east" overrides the legacy region ASC
			// tie-break. The upstream_fit INSERT beats the
			// region tie-break (cluster outline §D3 — placed
			// BETWEEN vCPU and region).
			name: "preferred region beats legacy tie-break",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "aardvark", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra")},
				{ID: "b-id", Name: "bison", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req, PreferredRegion: "us-east"},
			wantID: "b-id", // upstream_fit: b's region matches
		},
		{
			// Scenario C: bias breaks tie when only one
			// candidate matches. a and b have identical
			// (region="us-east"); PreferredRegion matches
			// both. The legacy name ASC tie-break decides
			// (a < b). This pins that upstream_fit does
			// NOT invent a false preference when both
			// candidates already match.
			name: "preferred region matching both falls through to name",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "aardvark", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east")},
				{ID: "b-id", Name: "bison", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req, PreferredRegion: "us-east"},
			wantID: "a-id", // both match → name tie-break
		},
		{
			// Scenario D: bias is skipped when the
			// PreferredRegion doesn't match either
			// candidate. Both regions are eu-fra and
			// us-east; PreferredRegion="ap-tokyo" matches
			// neither. The legacy region ASC tie-break
			// wins (eu-fra < us-east) — the bias is
			// strictly opt-in and does NOT promote a
			// "fallback" preference.
			name: "non-matching preferred region falls through to legacy",
			nodes: []state.ComputeNode{
				{ID: "a-id", Name: "aardvark", TargetURL: "unix:///a", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("eu-fra")},
				{ID: "b-id", Name: "bison", TargetURL: "unix:///b", AdmissionCeilingMB: 100, VCPUBudget: 160, Active: true, Region: strPtr("us-east")},
			},
			usedMB: map[string]int64{"a-id": 0, "b-id": 0},
			r:      Request{RAMMB: req, PreferredRegion: "ap-tokyo"},
			wantID: "a-id", // neither matches → legacy "eu-fra" < "us-east"
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChoosePlacement(tc.nodes, tc.usedMB, nil, tc.r)
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

func TestChoosePlacementPrefersReadySnapshotReplica(t *testing.T) {
	nodes := []state.ComputeNode{
		node("cached", "cached", 20, 100),
		node("uncached", "uncached", 0, 100),
	}
	placement, err := ChoosePlacement(nodes,
		map[string]int64{"cached": 20, "uncached": 0},
		map[string]int64{},
		Request{RAMMB: 40, PreferredNodeIDs: []string{"cached"}})
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if placement.NodeID != "cached" {
		t.Fatalf("node = %q, want cached", placement.NodeID)
	}
}

func TestChoosePlacementFallsThroughWhenReadyReplicaIsFull(t *testing.T) {
	nodes := []state.ComputeNode{
		node("cached", "cached", 90, 100),
		node("uncached", "uncached", 0, 100),
	}
	placement, err := ChoosePlacement(nodes,
		map[string]int64{"cached": 90, "uncached": 0},
		map[string]int64{},
		Request{RAMMB: 40, PreferredNodeIDs: []string{"cached"}})
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	if placement.NodeID != "uncached" {
		t.Fatalf("node = %q, want uncached", placement.NodeID)
	}
}

// TestChoosePlacement_RejectsNonPositiveRAM pins the "RAM must be
// positive" guard. A zero-RAM request would silently land on the first
// active node with zero check, which is wrong (every real instance
// carries at least the +8 MB overhead).
func TestChoosePlacement_RejectsNonPositiveRAM(t *testing.T) {
	nodes := []state.ComputeNode{node("a-id", "a", 0, 1000)}
	usedMB := map[string]int64{"a-id": 0}
	_, err := ChoosePlacement(nodes, usedMB, nil, Request{RAMMB: 0})
	if err == nil {
		t.Fatal("expected error for zero RAM")
	}
	_, err = ChoosePlacement(nodes, usedMB, nil, Request{RAMMB: -10})
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
		AdmissionCeilingMB: 100, VCPUBudget: 160,
		Lifecycle: state.NodeLifecycleActive, Active: true,
	}
	r := Request{RAMMB: 92} // billable = 100
	usedMB := map[string]int64{"tight": 0}

	if _, err := ChoosePlacement([]state.ComputeNode{ceilingNode}, usedMB, nil, r); err != nil {
		t.Errorf("100 MB ceiling should fit 100 MB billable, got %v", err)
	}
	ceilingNode.AdmissionCeilingMB = 99 // 100 > 99 → no fit
	if _, err := ChoosePlacement([]state.ComputeNode{ceilingNode}, usedMB, nil, r); err == nil {
		t.Error("99 MB ceiling must refuse 100 MB billable (overhead included)")
	}
}

// TestChoosePlacement_LifecycleFilter (Workstream B / issue
// #1184 / ADR-137) pins the lifecycle-aware filter. Nodes with
// lifecycle='draining' or 'unavailable' must be skipped even if
// Active=true (a stale bool could allow placement into a node
// the heartbeat already flipped). Lifecycle='recovering' stays
// admitting (matches the Postgres STORED GENERATED predicate).
func TestChoosePlacement_LifecycleFilter(t *testing.T) {
	const ramMB = 100
	const req = 64
	usedMB := map[string]int64{"a-id": 0, "b-id": 0, "c-id": 0, "d-id": 0}
	admitting := node("a-id", "active", 0, 100)
	admitting.Lifecycle = state.NodeLifecycleActive
	recovering := node("b-id", "recovering", 0, 100)
	recovering.Lifecycle = state.NodeLifecycleRecovering
	draining := node("c-id", "draining", 0, 100)
	draining.Lifecycle = state.NodeLifecycleDraining
	draining.Active = true // stale bool — must still be skipped
	unavailable := node("d-id", "unavailable", 0, 100)
	unavailable.Lifecycle = state.NodeLifecycleUnavailable
	unavailable.Active = true // stale bool — must still be skipped

	got, err := ChoosePlacement(
		[]state.ComputeNode{draining, unavailable, recovering, admitting},
		usedMB, nil, Request{RAMMB: req},
	)
	if err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}
	// Tie-break is name ASC; both active and recovering admit.
	// active < recovering lexically → "active" wins.
	if got.Name != "active" {
		t.Errorf("chose node=%q, want %q (lifecycle filter must skip draining + unavailable)", got.Name, "active")
	}
	// Single-draining-node fleet: no admission.
	if _, err := ChoosePlacement(
		[]state.ComputeNode{draining}, usedMB, nil, Request{RAMMB: req},
	); err == nil {
		t.Error("draining-only fleet must refuse admission")
	}
}
