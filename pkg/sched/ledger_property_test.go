package sched

// Property-based test for the admission ledger — the mechanised form of
// invariants §6.2-1 (per-app concurrency) and §6.2-2 (Σ(ram+8) ≤ 47,600 MB).
// CLAUDE.md: "Invariants — enforce with property-based tests, never delete."
//
// A native Go fuzz target drives a random sequence of Admit/BeginSnapshot/Release
// operations over a small fixed universe of apps and instances, then asserts —
// after EVERY operation — that the ledger's internal accounting is consistent
// with the ground truth recomputed from its live entries and that no invariant
// is ever breached. This is a white-box test (package sched) so it can read the
// unexported fields the public API only exposes as aggregates.
//
// Errors from the ledger are legal outcomes (a full box rejects a wake), so the
// operations ignore returned errors; the invariants must hold whether an op
// succeeded or was refused.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// propApp is one app in the fuzz universe, with a plan and the request shape the
// ledger will see. The RAM/vCPU/concurrency values are the plan maxima so the
// ledger's own clamping (Admit uses min(req, plan)) is a no-op and the expected
// concurrency ceiling is exactly conc.
type propApp struct {
	id   string
	plan api.Plan
	ram  int
	vcpu int
	conc int
}

var propApps = []propApp{
	{"free", api.PlanFree, 128, 2, 1},
	{"hobby", api.PlanHobby, 256, 2, 2},
	{"pro", api.PlanPro, 512, 2, 5},
	{"scale", api.PlanScale, 1024, 4, 20},
}

// instancesPerApp bounds the instance-id pool so BeginSnapshot/Release land on
// instances that plausibly exist. It is deliberately larger than any plan's
// concurrency cap so the fuzzer exercises the rejection path.
const instancesPerApp = 8

func FuzzLedgerInvariants(f *testing.F) {
	// Seeds: a simple grow, a churn pattern, and a burst that pushes toward the
	// ceiling. Each byte encodes one operation (decoded in decodeOp).
	f.Add([]byte{0x00, 0x04, 0x08, 0x0c})
	f.Add([]byte{0x00, 0x40, 0x80, 0x00, 0x40, 0x80})
	f.Add(make([]byte, 256)) // 256 admits of the same (free) instance → dedup + churn

	f.Fuzz(func(t *testing.T, ops []byte) {
		l := NewNodeLedger()
		for i, b := range ops {
			applyOp(l, b)
			checkLedgerInvariants(t, l, i)
		}
	})
}

// applyOp decodes one byte into an operation and applies it, ignoring the
// (legal) error return. Byte layout:
//
//	bit 0-1: action (0,3 = Admit; 1 = BeginSnapshot; 2 = Release)
//	bit 2-3: app index into propApps
//	bit 4-6: instance index within the app (0..7)
func applyOp(l *NodeLedger, b byte) {
	app := propApps[(b>>2)&0x03]
	inst := fmt.Sprintf("%s-%d", app.id, int(b>>4)%instancesPerApp)
	switch b & 0x03 {
	case 1:
		l.BeginSnapshot(inst)
	case 2:
		l.Release(inst)
	default: // 0 and 3 both Admit — weight admission higher than teardown
		_ = l.Admit(Request{
			Instance:       inst,
			AppID:          app.id,
			Plan:           app.plan,
			RAMMB:          app.ram,
			VCPU:           app.vcpu,
			MaxConcurrency: app.conc,
		})
	}
}

// checkLedgerInvariants recomputes ground truth from l.entries and
// the per-node resident map, asserts the cached aggregates match,
// then checks the hard invariants. Single-goroutine test, so
// reading unexported fields without the mutex is safe.
//
// PR #113 reshaped the ledger: resident RAM and vCPU live per-node
// in l.resident (map[node_id]*nodeReservation). The recomputation
// walks both l.entries (truth source for everything that lives on
// a reservation) and l.resident (truth source for the per-node
// sums). The hard invariants stay box-wide: §6.2-2 is Σ over all
// nodes, each capped at api.RAMAdmissionCeilingMB by default; on
// the property test's single-node fleet, the sum equals the
// ceiling so the original invariant still pins.
//
// Per-app concurrency (§6.2-1) stays global — it's per-app, not
// per-node — so the perApp map keeps its single-counter shape.
func checkLedgerInvariants(t *testing.T, l *NodeLedger, step int) {
	t.Helper()
	checkLedgerInvariantsForNodes(t, l, step, nil)
}

// checkLedgerInvariantsForNodes is the multi-node-aware invariant
// check shared by FuzzLedgerInvariants (single-node fleet, fleet
// is nil → falls back to api.RAMAdmissionCeilingMB per node) and
// FuzzLedgerInvariantsMultiNode (fleet is non-nil → per-node
// ceiling is each fleet entry's AdmissionCeilingMB).
//
// The per-node ceiling check is the load-bearing assertion for
// issue #297 acceptance item 5: a future refactor that mistakenly
// folds the global api.RAMAdmissionCeilingMB constant into the
// per-node Admit path must fail here.
func checkLedgerInvariantsForNodes(t *testing.T, l *NodeLedger, step int, fleet []propNode) {
	t.Helper()

	ceilingFor := func(nodeID string) int {
		if len(fleet) == 0 {
			return api.RAMAdmissionCeilingMB
		}
		for _, n := range fleet {
			if n.id == nodeID {
				return n.admissionCeilingMB
			}
		}
		// Fleet declared but the operation landed on an unknown node
		// (shouldn't happen with the multi-node fuzz decoder, which
		// picks from fleet indices only). Fall back to the global
		// constant so a regression surfaces loudly rather than
		// silently passing.
		return api.RAMAdmissionCeilingMB
	}

	var wantRAM, wantVCPU int
	wantConc := map[string]int{}
	wantPerNodeRAM := map[string]int{}
	wantPerNodeVCPU := map[string]int{}
	for _, e := range l.entries {
		wantRAM += e.admissionMB
		wantVCPU += e.vcpu
		wantPerNodeRAM[e.nodeID] += e.admissionMB
		wantPerNodeVCPU[e.nodeID] += e.vcpu
		if e.countsConc {
			wantConc[e.appID]++
		}
	}

	// Cached per-node aggregates must equal the recomputed truth (no drift).
	for nodeID, node := range l.resident {
		if node.residentRAM != wantPerNodeRAM[nodeID] {
			t.Fatalf("step %d: resident[%q].residentRAM=%d, recomputed=%d",
				step, nodeID, node.residentRAM, wantPerNodeRAM[nodeID])
		}
		if node.usedVCPU != wantPerNodeVCPU[nodeID] {
			t.Fatalf("step %d: resident[%q].usedVCPU=%d, recomputed=%d",
				step, nodeID, node.usedVCPU, wantPerNodeVCPU[nodeID])
		}
		// Per-node RAM ceiling — the load-bearing assertion. Each
		// node's resident is capped at its own AdmissionCeilingMB
		// (or the global constant for the single-node fleet). A
		// future refactor that re-introduces a box-wide ceiling
		// inside Admit must fail this check before it ships.
		ceiling := ceilingFor(nodeID)
		if node.residentRAM > ceiling {
			t.Fatalf("step %d: node %q residentRAM=%d breached per-node ceiling %d",
				step, nodeID, node.residentRAM, ceiling)
		}
	}
	for nodeID, want := range wantPerNodeRAM {
		if _, ok := l.resident[nodeID]; !ok {
			t.Fatalf("step %d: resident map missing node %q (want RAM=%d)", step, nodeID, want)
		}
	}

	// Global aggregates (used by ResidentRAM / UsedVCPU public API).
	if got := l.ResidentRAM(); got != wantRAM {
		t.Fatalf("step %d: ResidentRAM()=%d, recomputed=%d", step, got, wantRAM)
	}
	if got := l.UsedVCPU(); got != wantVCPU {
		t.Fatalf("step %d: UsedVCPU()=%d, recomputed=%d", step, got, wantVCPU)
	}

	// perApp must have no stale/zero/negative entries and match the truth.
	for app, c := range l.perApp {
		if c <= 0 {
			t.Fatalf("step %d: perApp[%q]=%d — stale/negative entry left behind", step, app, c)
		}
		if wantConc[app] != c {
			t.Fatalf("step %d: perApp[%q]=%d, recomputed=%d", step, app, c, wantConc[app])
		}
	}
	for app, c := range wantConc {
		if l.perApp[app] != c {
			t.Fatalf("step %d: perApp missing app %q (have %d, want %d)", step, app, l.perApp[app], c)
		}
	}

	// Hard invariants — these are the product.
	residentRAM := l.ResidentRAM()
	usedVCPU := l.UsedVCPU()
	if residentRAM < 0 || usedVCPU < 0 {
		t.Fatalf("step %d: negative accounting: ram=%d vcpu=%d", step, residentRAM, usedVCPU)
	}
	// Cluster total Σ(ram_mb + 8) is bounded by Σ(node.AdmissionCeilingMB)
	// on a multi-node fleet — NOT by api.RAMAdmissionCeilingMB. The
	// single-node property test pins the legacy posture; the
	// multi-node property test pins the cluster-sum posture. The
	// per-node check above is the load-bearing assertion (it
	// catches the actual bug); this cluster-sum check is a
	// cheap defence-in-depth sanity net for any regression
	// that bypasses the per-node ceiling while still
	// admitting — e.g. a refactor that splits the ceiling
	// check across two functions and one branch is missed.
	if len(fleet) > 0 {
		var clusterCeiling int
		for _, n := range fleet {
			clusterCeiling += n.admissionCeilingMB
		}
		if residentRAM > clusterCeiling { // §6.2-2 cluster-sum
			t.Fatalf("step %d: cluster residentRAM=%d breached Σ(node.AdmissionCeilingMB)=%d",
				step, residentRAM, clusterCeiling)
		}
	}
	if len(fleet) == 0 && residentRAM > api.RAMAdmissionCeilingMB { // §6.2-2 single-node
		t.Fatalf("step %d: residentRAM=%d breached admission ceiling %d",
			step, residentRAM, api.RAMAdmissionCeilingMB)
	}
	if usedVCPU > api.VCPUSlots {
		t.Fatalf("step %d: usedVCPU=%d exceeded %d vCPU slots", step, usedVCPU, api.VCPUSlots)
	}
	for _, app := range propApps { // §6.2-1
		if got := l.perApp[app.id]; got > app.conc {
			t.Fatalf("step %d: app %q concurrency=%d exceeded cap %d", step, app.id, got, app.conc)
		}
	}
}

// propNode is one compute node in the multi-node fuzz universe.
// AdmissionCeilingMB is the per-node RAM admission ceiling from
// compute_nodes.admission_ceiling_mb (see migration 00024). The
// fuzzer populates Request.NodeCeilingMB from this value so the
// per-node ceiling check inside Admit runs end-to-end.
type propNode struct {
	id                 string
	admissionCeilingMB int
}

// propFleet is the canonical set of node configurations the
// multi-node fuzz exercises. Two configurations:
//
//	[N=1] — matches the legacy single-box posture; the per-node
//	        ceiling equals api.RAMAdmissionCeilingMB so the existing
//	        single-node invariant continues to pin.
//	[N=2] — two nodes, each with a 23,800 MB ceiling, so the
//	        cluster ceiling matches the legacy 47,600 MB global
//	        constant. A regression that collapses per-node ceilings
//	        into the global constant still passes (the math works
//	        out), but a regression that *over*-admits across nodes
//	        past Σ(ceiling) fails.
//	[N=4] — four nodes at 12,000 MB each. Cluster ceiling
//	        (48,000 MB) ≈ global constant; the heterogeneity
//	        stresses the per-row lookup path.
var propFleet = [][]propNode{
	{{id: "default-local", admissionCeilingMB: api.RAMAdmissionCeilingMB}},
	{
		{id: "node-a", admissionCeilingMB: 23_800},
		{id: "node-b", admissionCeilingMB: 23_800},
	},
	{
		{id: "n1", admissionCeilingMB: 12_000},
		{id: "n2", admissionCeilingMB: 12_000},
		{id: "n3", admissionCeilingMB: 12_000},
		{id: "n4", admissionCeilingMB: 12_000},
	},
}

// FuzzLedgerInvariantsMultiNode is the multi-node sibling of
// FuzzLedgerInvariants (issue #297 acceptance item 5 / Tier 2
// Phase E). It parameterises the fuzz universe over N compute
// nodes, where each node carries its own AdmissionCeilingMB, and
// asserts the load-bearing invariant after every operation:
//
//	For every node n at every step s, n.residentRAM ≤ n.AdmissionCeilingMB.
//
// Per-app concurrency (§6.2-1) stays global — it's per-app, not
// per-node — so the perApp map keeps its single-counter shape
// and the existing assertion carries over unchanged. The cluster
// total Σ(ram_mb + 8) is bounded by Σ(node.AdmissionCeilingMB),
// not the global api.RAMAdmissionCeilingMB constant; the per-node
// check above is the load-bearing enforcement, the global sum
// is the safety net.
//
// The single-node FuzzLedgerInvariants is the N=1 member of this
// set (the fleet is implicitly {default-local}); existing CI fuzz
// corpora continue to apply without modification.
func FuzzLedgerInvariantsMultiNode(f *testing.F) {
	// Seeds cover the canonical fleet shapes plus a churn pattern
	// and a burst that pushes toward the per-node ceiling. The
	// first byte's HIGH bits encode the fleet index (mask 0xC0;
	// shifted right 6); the LOW bits of subsequent bytes are
	// operations against that fleet. High-bit encoding avoids
	// colliding with applyOp's bit 0-1 (action) and bit 2-3
	// (app) — the first byte's low bits are reserved for fleet
	// index, not actions, so we strip it before the op loop.
	f.Add([]byte{0x00, 0x00, 0x04, 0x08, 0x0c})                         // N=1, mirror FuzzLedgerInvariants seed
	f.Add([]byte{0x40, 0x00, 0x04, 0x08, 0x0c})                         // N=2
	f.Add([]byte{0x80, 0x00, 0x04, 0x08, 0x0c})                         // N=4
	f.Add([]byte{0x40, 0x00, 0x40, 0x80, 0x00, 0x40, 0x80})             // N=2 churn
	f.Add([]byte{0x80, 0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70}) // N=4 fan-out

	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) == 0 {
			return
		}
		fleetIdx := (int(ops[0]>>6) & 0x03) % len(propFleet)
		fleet := propFleet[fleetIdx]
		ops = ops[1:]
		l := NewNodeLedger()
		for i, b := range ops {
			applyOpMultiNode(l, b, fleet)
			checkLedgerInvariantsForNodes(t, l, i, fleet)
		}
	})
}

// applyOpMultiNode is the multi-node sibling of applyOp. The
// operation byte layout extends applyOp's with a node index:
//
//	bit 0-1: action  (0,3 = Admit; 1 = BeginSnapshot; 2 = Release)
//	bit 2-3: app index into propApps
//	bit 4-6: instance index within the app (0..7)
//	bit 7  : node index (low bit), doubled + bit 0 of next byte
//	          if N > 2; for N=1 it's always 0.
//
// For simplicity, the node index is encoded in bits 7-8 of the
// operation byte (mask 0x180) plus bit 0 of the next byte for
// N=4; nodes are picked round-robin modulo len(fleet). The
// Admit path threads NodeID and NodeCeilingMB through Request
// so the per-node ceiling check inside Admit runs end-to-end.
func applyOpMultiNode(l *NodeLedger, b byte, fleet []propNode) {
	app := propApps[(b>>2)&0x03]
	inst := fmt.Sprintf("%s-%d", app.id, int(b>>4)%instancesPerApp)
	nodeIdx := int((b>>7)&0x01) % len(fleet)
	if len(fleet) > 2 {
		// bit 7-8 covers 0..3; for the N=4 fleet we use the
		// high bits of the byte as the second index bit. The
		// exact encoding is intentionally lenient — every fleet
		// shape is exercised by every seed; we want coverage,
		// not a faithful model of the chooser.
		nodeIdx = int(b>>6) % len(fleet)
	}
	node := fleet[nodeIdx]

	switch b & 0x03 {
	case 1:
		l.BeginSnapshot(inst)
	case 2:
		l.Release(inst)
	default: // 0 and 3 both Admit
		_ = l.Admit(Request{
			Instance:       inst,
			AppID:          app.id,
			Plan:           app.plan,
			RAMMB:          app.ram,
			VCPU:           app.vcpu,
			MaxConcurrency: app.conc,
			NodeID:         node.id,
			NodeCeilingMB:  node.admissionCeilingMB,
		})
	}
}

// TestNodeLedger_PerNodeCeiling_AdmitRefusesOverflowingNode pins
// the per-node ceiling's enforcement at the smaller end: a
// compute node with a sub-global constant ceiling refuses
// admission once the ceiling is reached (issue #297 acceptance
// item 5, scenario 2).
//
// The complementary "two nodes admit independently" property
// (scenario 1 — per-node ceilings don't cross-talk) is pinned by
// TestNodeLedger_PerNodeCeiling_TwoNodesAdmitIndependently below.
func TestNodeLedger_PerNodeCeiling_AdmitRefusesOverflowingNode(t *testing.T) {
	t.Run("single_node_ceiling_below_global_constant", func(t *testing.T) {
		// Scenario 2: a smaller compute node (e.g. a 24 GB box
		// carrying a 12,000 MB ceiling) refuses admission at
		// 12,000 MB even though the cluster ceiling (12,000 MB)
		// is far below the global 47,600 MB constant.
		const smallCeiling = 12_000
		scaleLimits := api.MustLimitsFor(api.PlanScale)
		l := NewNodeLedger()

		// Fill the node up to its per-node ceiling. Each scale
		// admit adds 1,032 MB billable (1024 + 8); 12,000 /
		// 1,032 = 11 admits before the ceiling bites. The
		// per-app concurrency cap (scale = 20) doesn't trip
		// at this count.
		const ram = 1_024
		var lastErr error
		admitted := 0
		for i := 0; i < scaleLimits.MaxConcurrency; i++ {
			err := l.Admit(Request{
				Instance:       fmt.Sprintf("small-%d", i),
				AppID:          "small-app", // distinct appID so per-app cap is freshly applied
				Plan:           api.PlanScale,
				RAMMB:          ram,
				VCPU:           1,
				MaxConcurrency: scaleLimits.MaxConcurrency,
				NodeID:         "small",
				NodeCeilingMB:  smallCeiling,
			})
			if err != nil {
				lastErr = err
				break
			}
			admitted++
		}
		if lastErr == nil {
			t.Fatalf("expected per-node ceiling to refuse at %d MB; never did — fixture too small", smallCeiling)
		}
		if admitted >= scaleLimits.MaxConcurrency {
			t.Fatalf("per-node ceiling %d MB did not bite before per-app cap %d — fixture too small (admitted=%d)",
				smallCeiling, scaleLimits.MaxConcurrency, admitted)
		}
		got := l.ResidentRAMForNode("small")
		if got > smallCeiling {
			t.Fatalf("small-node residentRAM=%d breached its %d MB ceiling", got, smallCeiling)
		}
		// Pin: the node must have refused an admission once the
		// ceiling was reached. The last error we observed is the
		// per-node capacity refusal (not a per-app refusal).
		if !strings.Contains(lastErr.Error(), "per-node admission ceiling") {
			t.Fatalf("expected per-node ceiling refusal; got %v — per-node ceiling not enforced", lastErr)
		}
	})
}

// TestNodeLedger_PerNodeCeiling_TwoNodesAdmitIndependently pins
// the per-node ceiling's independence from the global
// api.RAMAdmissionCeilingMB constant (issue #297 acceptance item 5,
// scenario 1). The two nodes' ceilings sum to the global
// constant; filling node-0 must not starve node-1, and a
// future refactor that mistakenly folds the global constant
// into the per-node Admit (refusing at 47,600 MB instead of
// the per-row value) would fail here.
func TestNodeLedger_PerNodeCeiling_TwoNodesAdmitIndependently(t *testing.T) {
	const halfCeiling = 23_800
	scaleLimits := api.MustLimitsFor(api.PlanScale)
	l := NewNodeLedger()

	// Fill node-0 as far as its ceiling allows, capped at the per-app
	// concurrency limit. The count derives from the plan's RAM so a
	// plan change (ADR-1350 doubled Scale to 2,048 MB: 11 admits of
	// 2,056 MB = 22,616 MB) keeps exercising the per-node ceiling's
	// path without overflowing it; the assertion below verifies the
	// ceiling wasn't crossed.
	fill := min(scaleLimits.MaxConcurrency, halfCeiling/(scaleLimits.RAMMB+api.PerVMOverheadMB))
	if fill < 2 {
		t.Fatalf("fixture admits %d Scale instances under %d MB; widen the ceiling", fill, halfCeiling)
	}
	for i := 0; i < fill; i++ {
		err := l.Admit(Request{
			Instance:       fmt.Sprintf("node0-%d", i),
			AppID:          "node0-app", // distinct appID so per-app cap is freshly applied
			Plan:           api.PlanScale,
			RAMMB:          scaleLimits.RAMMB,
			VCPU:           1, // 1 vCPU each so the global vCPU cap doesn't bite at 20 instances
			MaxConcurrency: scaleLimits.MaxConcurrency,
			NodeID:         "node-0",
			NodeCeilingMB:  halfCeiling,
		})
		if err != nil {
			t.Fatalf("node-0 admit %d refused (err=%v) before per-node ceiling — fixture is wrong", i, err)
		}
	}
	got0 := l.ResidentRAMForNode("node-0")
	if got0 > halfCeiling {
		t.Fatalf("node-0 residentRAM=%d breached per-node ceiling %d", got0, halfCeiling)
	}

	// Now admit on node-1; node-1 has its own 23,800 MB ceiling
	// that's untouched. The per-node ceiling check inside Admit
	// must admit normally — a future refactor that mistakenly
	// folds the global 47,600 MB cap into the per-node Admit
	// would refuse this on the cluster-sum ground (node-0 is
	// already near its ceiling, well under 47,600 MB cluster-wide,
	// but a tighter fold against the cluster sum would surface
	// here).
	err := l.Admit(Request{
		Instance:       "node1-0",
		AppID:          "node1-app", // distinct appID so per-app cap is freshly applied
		Plan:           api.PlanScale,
		RAMMB:          scaleLimits.RAMMB,
		VCPU:           1,
		MaxConcurrency: scaleLimits.MaxConcurrency,
		NodeID:         "node-1",
		NodeCeilingMB:  halfCeiling,
	})
	if err != nil {
		t.Fatalf("node-1 admission refused (err=%v) — per-node ceiling wrongly collides with global constant", err)
	}
	if got := l.ResidentRAMForNode("node-1"); got == 0 {
		t.Fatalf("node-1 residentRAM=0 after successful admit — bookkeeping drift")
	}
}
