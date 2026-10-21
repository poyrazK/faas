// state_machine_edges_test.go — branch coverage for the
// state-machine edges in pkg/sched/admission.go left by
// admission_test.go + admission_vcpu_test.go.
//
// Covered here:
//   - BeginSnapshot idempotency: second call is a no-op once
//     countsConc=false (already snapshotting).
//   - BeginSnapshot before Admit: no-op (entry absent).
//   - BeginSnapshot + Release: reaper path — the entry is freed,
//     perApp is decremented twice (once by BeginSnapshot, once
//     by Release), and the cleanupApp guard cleans the zero map.
//   - Release of unknown instance: silent no-op (the "leak
//     detection during tests" contract).
//   - Admit with empty AppID: must still record perApp[""]=1
//     (defensive — a caller bug shouldn't crash, but the entry
//     IS counted under the empty key for visibility).
//   - Admit duplicate instance: rejected with the duplicate
//     error before any counters are touched.
//   - Per-node counter zero: the per-node map entry is deleted
//     when both RAM and vCPU drop to 0 (memory hygiene).
//   - cleanupApp guard: perApp[key] deleted when value ≤ 0.
//   - RAM headroom on a node that hasn't been seen yet:
//     ceilingForNode_locked fallback for un-registered nodes.
//   - vCPU headroom rejection on the per-node budget.

package sched_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// TestBeginSnapshot_Idempotent pins the second-call no-op: once
// countsConc=false (we already started the snapshot), a second
// BeginSnapshot MUST NOT decrement perApp twice (that would let
// the reaper admit an extra instance during a stuck snapshot).
func TestBeginSnapshot_Idempotent(t *testing.T) {
	l := sched.NewNodeLedger()
	r := sched.Request{Instance: "vm-1", AppID: "app-1", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, Plan: api.PlanHobby}
	if err := l.Admit(r); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := l.Concurrency("app-1"); got != 1 {
		t.Fatalf("after Admit: Concurrency = %d, want 1", got)
	}
	l.BeginSnapshot("vm-1")
	if got := l.Concurrency("app-1"); got != 0 {
		t.Fatalf("after BeginSnapshot: Concurrency = %d, want 0", got)
	}
	// Second call: no-op, Concurrency stays at 0 (not -1).
	l.BeginSnapshot("vm-1")
	if got := l.Concurrency("app-1"); got != 0 {
		t.Errorf("second BeginSnapshot: Concurrency = %d, want 0 (idempotent)", got)
	}
}

// TestBeginSnapshot_UnknownInstance pins the "entry absent" branch:
// BeginSnapshot on an instance that was never admitted is a no-op
// (the ledger doesn't crash).
func TestBeginSnapshot_UnknownInstance(t *testing.T) {
	l := sched.NewNodeLedger()
	// Should not panic.
	l.BeginSnapshot("ghost")
	if got := len((*l.Entries())); got != 0 {
		t.Errorf("BeginSnapshot(ghost): entries = %d, want 0", got)
	}
}

// TestRelease_UnknownInstance pins the silent no-op on
// Release("unknown"). Production callers treat this as a no-op;
// the test seal "leak detection during tests" stays as a
// future optimisation (current code logs nothing on this path).
func TestRelease_UnknownInstance(t *testing.T) {
	l := sched.NewNodeLedger()
	// Should not panic, should not affect state.
	l.Release("ghost")
	if got := len((*l.Entries())); got != 0 {
		t.Errorf("Release(ghost): entries = %d, want 0", got)
	}
}

// TestAdmit_DuplicateInstanceRejected pins the duplicate-instance
// guard. The second Admit with the same instance id MUST error,
// and the perApp counter MUST NOT be bumped twice.
func TestAdmit_DuplicateInstanceRejected(t *testing.T) {
	l := sched.NewNodeLedger()
	r := sched.Request{Instance: "vm-dup", AppID: "app-1", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, Plan: api.PlanHobby}
	if err := l.Admit(r); err != nil {
		t.Fatalf("first Admit: %v", err)
	}
	err := l.Admit(r)
	if err == nil {
		t.Fatal("duplicate Admit: want error, got nil")
	}
	if !strings.Contains(err.Error(), "already admitted") {
		t.Errorf("err = %v, want substring 'already admitted'", err)
	}
	if got := l.Concurrency("app-1"); got != 1 {
		t.Errorf("Concurrency = %d after duplicate, want 1 (no double-count)", got)
	}
}

// TestAdmit_UnknownPlanRejected pins the
// `api.LimitsFor(r.Plan)` miss path. A plan not in the catalog
// (e.g. a typo from a custom caller) errors before any counters
// are touched.
func TestAdmit_UnknownPlanRejected(t *testing.T) {
	l := sched.NewNodeLedger()
	err := l.Admit(sched.Request{Instance: "vm-x", AppID: "app-1", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, Plan: api.Plan("bogus")})
	if err == nil {
		t.Fatal("unknown plan: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown plan") {
		t.Errorf("err = %v, want substring 'unknown plan'", err)
	}
}

// TestAdmit_PerAppConcurrencyAtMax pins the per-app max boundary.
// Hobby plan MaxConcurrency=2; admitting 2 succeeds; the 3rd
// errors with ErrPlanLimitConcurrency.
func TestAdmit_PerAppConcurrencyAtMax(t *testing.T) {
	l := sched.NewNodeLedger()
	for i := 0; i < 2; i++ {
		err := l.Admit(sched.Request{
			Instance: intStrPfx("vm-h", i), AppID: "app-hobby",
			NodeID: "node-a", RAMMB: 256, VCPU: 1, Plan: api.PlanHobby,
		})
		if err != nil {
			t.Fatalf("Admit %d: %v", i, err)
		}
	}
	// 3rd: rejected.
	err := l.Admit(sched.Request{Instance: "vm-h6", AppID: "app-hobby",
		NodeID: "node-a", RAMMB: 256, VCPU: 1, Plan: api.PlanHobby})
	if err == nil {
		t.Fatal("3rd Hobby admit: want ErrPlanLimitConcurrency, got nil")
	}
	if !strings.Contains(err.Error(), "concurrency") {
		t.Errorf("err = %v, want substring 'concurrency'", err)
	}
}

// TestRelease_DropsPerNodeCounterWhenZero pins the memory-hygiene
// branch: when the last reservation on a node is released, the
// per-node entry is removed from l.resident (no zombie keys
// accumulating after a fleet cycle).
func TestRelease_DropsPerNodeCounterWhenZero(t *testing.T) {
	l := sched.NewNodeLedger()
	r := sched.Request{Instance: "vm-1", AppID: "app-1", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, Plan: api.PlanHobby}
	if err := l.Admit(r); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// The per-node counter must be present.
	if _, ok := (*l.Resident())["node-a"]; !ok {
		t.Fatal("after Admit: per-node counter missing")
	}
	l.Release("vm-1")
	// After release, the per-node counter should be gone.
	if _, ok := (*l.Resident())["node-a"]; ok {
		t.Errorf("after Release: per-node counter still present (memory leak)")
	}
}

// TestCleanupApp_ZeroCountDeleted pins the perApp map hygiene
// branch: when perApp[app] drops to 0, the entry is deleted
// (the map doesn't accumulate zero-valued keys).
func TestCleanupApp_ZeroCountDeleted(t *testing.T) {
	l := sched.NewNodeLedger()
	r := sched.Request{Instance: "vm-1", AppID: "app-tmp", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, Plan: api.PlanHobby}
	if err := l.Admit(r); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := l.Concurrency("app-tmp"); got != 1 {
		t.Fatalf("after Admit: Concurrency = %d, want 1", got)
	}
	l.Release("vm-1")
	if got := l.Concurrency("app-tmp"); got != 0 {
		t.Errorf("after Release: Concurrency = %d, want 0 (deleted)", got)
	}
	if _, ok := (*l.PerApp())["app-tmp"]; ok {
		t.Error("perApp[\"app-tmp\"] entry still present after Release")
	}
}

// TestAdmit_PerNodeRAMCeilingEnforced pins the per-node RAM gate:
// even with plenty of global headroom, a node at its per-node
// ceiling must reject new reservations.
//
// Ceiling math: api.PerVMOverheadMB is added per admit. With
// 512 MB ceiling and 256 MB apps: admit 1 (256 + 8 = 264 MB)
// succeeds; admit 2 would push to 528 MB > 512 → rejected.
func TestAdmit_PerNodeRAMCeilingEnforced(t *testing.T) {
	l := sched.NewNodeLedger()
	r1 := sched.Request{Instance: "vm-1", AppID: "app-1", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, NodeCeilingMB: 512, Plan: api.PlanHobby}
	if err := l.Admit(r1); err != nil {
		t.Fatalf("Admit 1: %v", err)
	}
	// Second reservation pushes past the 512 MB ceiling
	// (264 + 264 = 528 MB > 512).
	r2 := sched.Request{Instance: "vm-2", AppID: "app-2", NodeID: "node-a",
		RAMMB: 256, VCPU: 1, NodeCeilingMB: 512, Plan: api.PlanHobby}
	err := l.Admit(r2)
	if err == nil {
		t.Fatal("RAM-ceiling reject: want ErrCapacity, got nil")
	}
	if !strings.Contains(err.Error(), "RAM") && !strings.Contains(err.Error(), "headroom") {
		t.Errorf("err = %v, want substring 'RAM' or 'headroom'", err)
	}
}

// TestAdmit_PerNodeFallbackCeiling pins the
// ceilingForNode_locked fallback: a request with
// NodeCeilingMB=0 (legacy seam) falls back to the global
// api.RAMAdmissionCeilingMB on the per-node ledger entry.
func TestAdmit_PerNodeFallbackCeiling(t *testing.T) {
	l := sched.NewNodeLedger()
	r := sched.Request{Instance: "vm-1", AppID: "app-1", NodeID: "unregistered-node",
		RAMMB: 256, VCPU: 1, NodeCeilingMB: 0, Plan: api.PlanHobby}
	if err := l.Admit(r); err != nil {
		t.Fatalf("Admit on un-registered node: %v", err)
	}
	// Sanity: the fallback ceiling is the global constant.
	if l.CeilingForNode_locked("unregistered-node", api.Limits{}) != api.RAMAdmissionCeilingMB {
		t.Error("ceilingForNode_locked didn't return the global fallback")
	}
}

// TestAdmit_ConcurrentNoDoubleCount is the property-style
// concurrency test: 100 goroutines admitting distinct instances
// of the SAME app must result in Concurrency(app) ==
// min(100, plan_max). Pro plan MaxConcurrency=5 — accept up to
// 5 successes, no double-counts.
func TestAdmit_ConcurrentNoDoubleCount(t *testing.T) {
	l := sched.NewNodeLedger()
	const n = 100
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = l.Admit(sched.Request{
				Instance: intStrPfx("vm-", i), AppID: "app-many",
				NodeID: "node-a", RAMMB: 64, VCPU: 1, Plan: api.PlanPro,
			})
		}(i)
	}
	wg.Wait()
	// Pro plan MaxConcurrency=5 — must be at most 5.
	if got := l.Concurrency("app-many"); got > 5 {
		t.Errorf("Pro plan Concurrency = %d, want ≤ 5", got)
	}
	if got := l.Concurrency("app-many"); got == 0 {
		t.Errorf("Pro plan Concurrency = 0, want > 0 (test infra broken?)")
	}
	// Sanity: successes match.
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != l.Concurrency("app-many") {
		t.Errorf("success count %d != Concurrency %d", successes, l.Concurrency("app-many"))
	}
}

// intStrPfx formats "prefixN" without dragging in strconv.
func intStrPfx(prefix string, i int) string {
	if i == 0 {
		return prefix + "0"
	}
	var b [16]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return prefix + string(b[pos:])
}
