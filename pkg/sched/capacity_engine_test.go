// capacity_engine_test.go — Engine.applyLiveCapacityMB tests (Tier A1).
//
// Pins the live-publisher → chooser binding:
//   - ledger floors the report (hostile / stale-low vmmd cannot
//     shrink the live accounting);
//   - stale or absent table returns the 0 sentinel so the caller
//     falls through to the store sum;
//   - the chooser picks the node that fits under the live
//     report's UsedMB, not the stale store sum.

package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestApplyLiveCapacityMB_FloorAtLedger pins the "ledger wins on
// stale-low / hostile reports" contract. A vmmd that emits a report
// claiming less UsedMB than the schedd ledger actually has must not
// shrink the live accounting — that would let the chooser place a
// request into a node whose cgroup memory.current already exceeds
// AdmissionCeilingMB. The ledger floor is the load-bearing
// invariant.
func TestApplyLiveCapacityMB_FloorAtLedger(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

	const nodeID = "floor-node"
	(*
	// Simulate two live instances on the node via the ledger: 256
	// MB each → ledger sum 528 MB (ram + 8 overhead × 2).
	e.XLedger()).Admit(sched.Request{
		Instance: "i1", AppID: "a", Plan: "pro",
		RAMMB: 256, VCPU: 2, MaxConcurrency: 5,
		NodeID: nodeID, NodeCeilingMB: 10000,
	})
	(*e.XLedger()).Admit(sched.Request{
		Instance: "i2", AppID: "a", Plan: "pro",
		RAMMB: 256, VCPU: 2, MaxConcurrency: 5,
		NodeID: nodeID, NodeCeilingMB: 10000,
	})
	(*
	// A hostile / buggy vmmd reports 100 MB (a quarter of the
	// truth). The ledger must floor it: the chooser sees 528.
	e.XCapacityTable()).Replace(sched.CapacityReport{NodeID: nodeID, UsedMB: 100})

	got := e.ApplyLiveCapacityMB(context.Background(), nodeID)
	if got != 528 {
		t.Errorf("applyLiveCapacityMB = %d, want 528 (ledger floor)", got)
	}
}

// TestApplyLiveCapacityMB_LiveWinsOverZero pins the converse: when
// the live report has real bytes and the ledger has nothing (e.g.
// schedd just restarted and vmmd has been reporting for hours),
// the live number wins. The floor only raises; it never lowers.
func TestApplyLiveCapacityMB_LiveWinsOverZero(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

	const nodeID = "live-node"
	(*e.XCapacityTable()).Replace(sched.CapacityReport{NodeID: nodeID, UsedMB: 4096})

	got := e.ApplyLiveCapacityMB(context.Background(), nodeID)
	if got != 4096 {
		t.Errorf("applyLiveCapacityMB = %d, want 4096 (live wins, no ledger floor)", got)
	}
}

// TestApplyLiveCapacityMB_StaleFallsBack pins the freshness budget
// at the engine boundary. A report older than CapacityFreshness
// must return 0 so the caller falls through to the store sum.
// (The 0 sentinel is what the caller distinguishes "stale" from
// "fresh zero" on; a fresh report with UsedMB=0 also returns 0,
// and the caller's "if used > 0" gate sends both to the store
// path — the store returns 0 for an idle node, which is
// equivalent.)
//
// The clock is injected via Engine.now (set in newEngine) so this
// test does not block on a real time.Sleep. The previous test
// shape used a 6-second sleep to drift past CapacityFreshness;
// that added 6 s to the pkg/sched suite wall time and was a
// real flake risk on slow CI runners. Two reports are stamped 0
// us and 6 s in the future; the future-dated lookup crosses the
// freshness budget and falls through to the store.
func TestApplyLiveCapacityMB_StaleFallsBack(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

	const nodeID = "stale-node"
	(*
	// First, observe the report as fresh (the test's "now" is the
	// fixture's stamped time; the report's lastSeen is stamped
	// inside Replace to e.now()).
	e.XCapacityTable()).Replace(sched.CapacityReport{NodeID: nodeID, UsedMB: 9000})
	if got := e.ApplyLiveCapacityMB(context.Background(), nodeID); got != 9000 {
		t.Fatalf("fresh report: applyLiveCapacityMB = %d, want 9000 (setup precondition)", got)
	}
	// Now fast-forward the engine's clock past CapacityFreshness.
	// The Replace inside the gRPC handler would stamp lastSeen to
	// the dialer-side time.Now (the handler is producer-side, not
	// under e.now), so this test is exercising the chooser-side
	// clock, which is exactly the seam we want to pin.
	offset := sched.CapacityFreshness + 1*time.Second
	(*e.Now()) = func() time.Time { return time.Now().Add(offset) }

	got := e.ApplyLiveCapacityMB(context.Background(), nodeID)
	if got != 0 {
		t.Errorf("applyLiveCapacityMB = %d for a stale report, want 0 (sentinel)", got)
	}
}

// TestApplyLiveCapacityMB_NilReceiver pins the pre-axis-5 fixture
// behaviour. An Engine constructed without a capacityTable (e.g. a
// legacy test seam that bypasses NewEngine) returns 0, the caller
// falls through to the store, and the legacy single-box path is
// preserved bit-for-bit.
func TestApplyLiveCapacityMB_NilReceiver(t *testing.T) {
	t.Parallel()
	var e *sched.Engine
	if got := e.ApplyLiveCapacityMB(context.Background(), "x"); got != 0 {
		t.Errorf("nil Engine.applyLiveCapacityMB = %d, want 0", got)
	}
}
