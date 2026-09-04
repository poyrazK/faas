// pgbackend_pernode_test.go — multi-node picker tests for the
// placement scheduler (ADR-025 axis 3).
//
// A target is already routable when it enters the gateway cache. The
// request picker therefore round-robins across all targets, including
// targets on different nodes. Warm affinity belongs to admission placement;
// it must not pin request traffic to one node after a burst has fanned out.
//
// These tests pin that contract at the unit level. They use a
// FakeScheduler (gateway.NewFakeScheduler) but vary its returned
// NodeID per Admit call via a small shim, since the public
// FakeScheduler pins a single NodeID.

package gateway_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// rotatingScheduler is a controllable scheduler that emits a
// different NodeID per call (minted from a closure). Used to seed
// the per-app targetSet with heterogeneous nodes so the picker has
// to make a real per-node choice rather than degenerate to the
// single-node fast path.
type rotatingScheduler struct {
	nextNodeID func() string // returns the node id for the next Admit call
	calls      atomic.Int64  // admitted count; ids are i-1..i-N
	method     int32         // raw wake method for Admit
}

func (r *rotatingScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	idx := r.calls.Add(1)
	nodeID := r.nextNodeID()
	// Scheduler signature (issue #460 / ADR-053 PR-C + issue #556 PR-B):
	// (instanceID, nodeID, deploymentID, wakeID, method, atCapacity, port, err).
	// Port=0 → legacy 8080 default at vmmd's buildBridgeScript boundary.
	// deploymentID="" → legacy single-deployment mode (picker collapses
	// to today's single-targetSet behaviour).
	return "i-" + strconv.FormatInt(idx, 10), nodeID, "", "wake-" + strconv.FormatInt(idx, 10), r.method, false, 0, nil
}

// EnsureWake (ADR-098) mirrors AdmitInstance. The FakeScheduler-style
// "fresh identity per call" is exactly what per-node multi-box tests
// want — the schedd-side leader/follower contract is pinned by the
// property-based test on the Engine side, not here.
func (r *rotatingScheduler) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	idx := r.calls.Add(1)
	nodeID := r.nextNodeID()
	return "i-" + strconv.FormatInt(idx, 10), nodeID, "", "wake-" + strconv.FormatInt(idx, 10), r.method, 0, nil
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) — test fake
// satisfies the widened Scheduler interface. Mirror tests live
// in pkg/gateway/handler_mirror_test.go; per-node rotation doesn't
// exercise the mirror hot path so a single endpoint suffices.
func (r *rotatingScheduler) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	idx := r.calls.Add(1)
	return "mirror-" + strconv.FormatInt(idx, 10), "wake-mirror-" + strconv.FormatInt(idx, 10), nil
}

// TestPGBackend_PickWeightsEveryHealthyTarget seeds two nodes with
// different healthy counts (a has 3, b has 1) and asserts global
// round-robin. A's three targets receive three quarters of the picks and
// B's target receives the remaining quarter.
func TestPGBackend_PickWeightsEveryHealthyTarget(t *testing.T) {
	// First 3 admits → node A; last admit → node B.
	admitIdx := atomic.Int64{}
	sched := &rotatingScheduler{
		nextNodeID: func() string {
			n := admitIdx.Add(1)
			if n <= 3 {
				return "node-A"
			}
			return "node-B"
		},
		method: gateway.WireWakeColdBoot,
	}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	// Seed 4 admits (3 on A, 1 on B). HealthyCount is total.
	for i := 0; i < 4; i++ {
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
			t.Fatalf("Admit #%d: %v", i+1, err)
		}
	}
	if got := b.HealthyCount("app-1"); got != 4 {
		t.Fatalf("HealthyCount = %d, want 4", got)
	}

	// Global round-robin visits the four targets in insertion order.
	seen := map[string]bool{}
	nodeCounts := map[string]int{}
	for i := 0; i < 8; i++ {
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		seen[t1.Target.InstanceID] = true
		nodeCounts[t1.Target.NodeID]++
	}
	if len(seen) != 4 {
		t.Errorf("Pick visited %d distinct targets, want 4", len(seen))
	}
	if nodeCounts["node-A"] != 6 || nodeCounts["node-B"] != 2 {
		t.Errorf("Pick node distribution = %#v, want node-A=6/node-B=2", nodeCounts)
	}
}

// TestPGBackend_PickDoesNotPinWarmAffinityNode seeds two nodes with equal
// healthy counts and configures a warm hint for node-B. The hint must not
// pin all request traffic to B; both nodes remain routable.
func TestPGBackend_PickDoesNotPinWarmAffinityNode(t *testing.T) {
	admitIdx := atomic.Int64{}
	sched := &rotatingScheduler{
		nextNodeID: func() string {
			n := admitIdx.Add(1)
			// Alternate: A, B, A, B → 2 on each.
			if n%2 == 1 {
				return "node-A"
			}
			return "node-B"
		},
		method: gateway.WireWakeColdBoot,
	}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)
	b.WithWarmHint(func(appID string) (string, bool) {
		if appID == "app-1" {
			return "node-B", true
		}
		return "", false
	})

	for i := 0; i < 4; i++ {
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
			t.Fatalf("Admit #%d: %v", i+1, err)
		}
	}

	nodeCounts := map[string]int{}
	for i := 0; i < 12; i++ {
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		nodeCounts[t1.Target.NodeID]++
	}
	if nodeCounts["node-A"] != 6 || nodeCounts["node-B"] != 6 {
		t.Errorf("Pick node distribution = %#v, want node-A=6/node-B=6", nodeCounts)
	}
}

// TestPGBackend_PickColdPathRoundRobinsNodes seeds two nodes with equal
// counts and no warm hint. The picker must use both routable nodes.
func TestPGBackend_PickColdPathRoundRobinsNodes(t *testing.T) {
	admitIdx := atomic.Int64{}
	sched := &rotatingScheduler{
		nextNodeID: func() string {
			// Insert order matters only for the first global pick.
			n := admitIdx.Add(1)
			if n == 1 {
				return "node-B"
			}
			return "node-A"
		},
		method: gateway.WireWakeColdBoot,
	}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)
	// No WithWarmHint set → picker still rotates across both nodes.

	for i := 0; i < 2; i++ {
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
			t.Fatalf("Admit #%d: %v", i+1, err)
		}
	}

	nodeCounts := map[string]int{}
	for i := 0; i < 6; i++ {
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		nodeCounts[t1.Target.NodeID]++
	}
	if nodeCounts["node-A"] != 3 || nodeCounts["node-B"] != 3 {
		t.Errorf("Pick node distribution = %#v, want node-A=3/node-B=3", nodeCounts)
	}
}

// TestPGBackend_PickSingleNodeFastPath seeds a single node with
// multiple instances and asserts Pick returns each instance in turn
// via the legacy single-cursor path (no per-node map allocation).
// This is the one-box degenerate case — must keep working without
// the per-node machinery allocating a map for one entry.
func TestPGBackend_PickSingleNodeFastPath(t *testing.T) {
	sched := gateway.NewFakeScheduler("node-A") // mints i-1, i-2, i-3 by default
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	for i := 0; i < 3; i++ {
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
			t.Fatalf("Admit #%d: %v", i+1, err)
		}
	}
	if got := b.HealthyCount("app-1"); got != 3 {
		t.Fatalf("HealthyCount = %d, want 3", got)
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		if t1.Target.NodeID != "node-A" {
			t.Errorf("Pick #%d NodeID = %q, want node-A", i, t1.Target.NodeID)
		}
		seen[t1.Target.InstanceID] = true
	}
	if len(seen) != 3 {
		t.Errorf("distinct picks = %d, want 3 (round-robin across the single node)", len(seen))
	}
}

// TestPGBackend_PickAfterEvictNodeEntry prunes cache metadata when the last
// entry for a node is evicted. Subsequent picks must come from the surviving
// node only.
func TestPGBackend_PickAfterEvictNodeEntry(t *testing.T) {
	admitIdx := atomic.Int64{}
	sched := &rotatingScheduler{
		nextNodeID: func() string {
			// 2 on A (i-1, i-2), 1 on B (i-3).
			n := admitIdx.Add(1)
			if n <= 2 {
				return "node-A"
			}
			return "node-B"
		},
		method: gateway.WireWakeColdBoot,
	}
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil)

	for i := 0; i < 3; i++ {
		if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 5); err != nil {
			t.Fatalf("Admit #%d: %v", i+1, err)
		}
	}

	// Drain node-A: evict i-1 and i-2 (the two A instances).
	b.EvictInstance("app-1", "i-1")
	b.EvictInstance("app-1", "i-2")

	// Only node-B's i-3 should be reachable now.
	if got := b.HealthyCount("app-1"); got != 1 {
		t.Fatalf("HealthyCount after draining A = %d, want 1", got)
	}
	for i := 0; i < 4; i++ {
		t1 := b.Pick("app-1")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		if t1.Target.InstanceID != "i-3" {
			t.Errorf("Pick #%d returned InstanceID = %q, want i-3 (the surviving node-B entry)", i, t1.Target.InstanceID)
		}
	}
}

// TestPGBackend_PickFollowsWarmHintCache wires a real
// *gateway.WarmHintCache (the same type cmd/gatewayd-internal/constructs)
// as the picker's WarmHintFunc, then drives cache.Update the way
// the warmHintConsumer would after a StreamWarmHints event. End-
// to-end picker coverage for the WarmHintCache → HintFunc →
// pick → Target.NodeID path.
//
// Two nodes seeded with equal healthy counts (2 each). The cache
// is initially empty (cold-start, ADR-005) — picker falls through
// to global round-robin. cache.Update("app", "node-B") must not change
// request distribution; the cache remains an admission-placement hint.
func TestPGBackend_PickFollowsWarmHintCache(t *testing.T) {
	admitIdx := atomic.Int64{}
	sched := &rotatingScheduler{
		nextNodeID: func() string {
			n := admitIdx.Add(1)
			if n%2 == 1 {
				return "node-A"
			}
			return "node-B"
		},
		method: gateway.WireWakeColdBoot,
	}
	cache := gateway.NewWarmHintCache()
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, sched, nil).
		WithWarmHint(cache.HintFunc())

	// Seed: 2 admits on each node (A, B, A, B → 2/2 split).
	for i := 0; i < 4; i++ {
		if _, _, _, err := b.Admit(context.Background(), "app", "", "", "", 5); err != nil {
			t.Fatalf("Admit #%d: %v", i+1, err)
		}
	}

	// Phase 1: empty cache. Both nodes are routable.
	phase1 := map[string]int{}
	for i := 0; i < 4; i++ {
		t1 := b.Pick("app")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		phase1[t1.Target.NodeID]++
	}
	if phase1["node-A"] != 2 || phase1["node-B"] != 2 {
		t.Errorf("phase 1 node distribution = %#v, want 2/2", phase1)
	}

	// Phase 2: hint flips to node-B. Request distribution remains balanced.
	cache.Update("app", "node-B")
	phase2 := map[string]int{}
	for i := 0; i < 4; i++ {
		t1 := b.Pick("app")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		phase2[t1.Target.NodeID]++
	}
	if phase2["node-A"] != 2 || phase2["node-B"] != 2 {
		t.Errorf("phase 2 node distribution = %#v, want 2/2", phase2)
	}

	// Phase 3: Forget clears the hint; request distribution is unchanged.
	cache.Forget("app")
	phase3 := map[string]int{}
	for i := 0; i < 4; i++ {
		t1 := b.Pick("app")
		if !t1.OK {
			t.Fatal("Pick: !ok")
		}
		phase3[t1.Target.NodeID]++
	}
	if phase3["node-A"] != 2 || phase3["node-B"] != 2 {
		t.Errorf("phase 3 node distribution = %#v, want 2/2", phase3)
	}
}
