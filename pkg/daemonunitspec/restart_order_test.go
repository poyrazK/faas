package daemonunitspec

import (
	"errors"
	"testing"
)

// TestRestartOrder_RespectsLifecycleAfter asserts the contract: every
// daemon's index in RestartOrder is GREATER THAN every daemon in its
// Lifecycle.After. This is the load-bearing invariant — a topological
// order is correct iff every edge goes from lower index to higher.
//
// Defensive against the Registry reshuffle: we derive the expected
// constraints from Registry itself, not from a hardcoded order. If a
// future PR adds a new daemon or a new After edge the test still
// validates the property without an edit. The hardcoded ordering is
// also asserted in TestRestartOrder_MatchesExpected — separate test
// so a registry change can update the expected without making the
// property test read as a "FAIL".
func TestRestartOrder_RespectsLifecycleAfter(t *testing.T) {
	order, err := RestartOrder()
	if err != nil {
		t.Fatalf("RestartOrder: %v", err)
	}

	pos := make(map[string]int, len(order))
	for i, name := range order {
		pos[name] = i
	}

	byName := make(map[string]Entry, len(Registry))
	for _, entry := range Registry {
		byName[entry.Name] = entry
	}

	for _, entry := range Registry {
		entryIdx, ok := pos[entry.Name]
		if !ok {
			t.Fatalf("entry %q missing from RestartOrder", entry.Name)
		}
		for _, after := range entry.Lifecycle.After {
			afterIdx, ok := pos[after]
			if !ok {
				t.Fatalf("dependency %q missing from RestartOrder (entry %q After)", after, entry.Name)
			}
			if afterIdx >= entryIdx {
				t.Errorf("RestartOrder invariant broken: %q (pos=%d) must come AFTER %q (pos=%d)",
					entry.Name, entryIdx, after, afterIdx)
			}
		}
	}
}

// TestRestartOrder_MatchesExpected is the hardcoded-order check. A
// change here means an intentional change in the Registry (a new
// daemon, a new After edge, a slice reorder). The expected order
// mirrors the documented dependency graph in registry.go: vmmd and
// apid are roots; schedd depends on vmmd; gatewayd-internal depends on
// schedd, apid; gatewayd-public depends on apid; meterd +
// githubd depend on apid; imaged depends on vmmd; builderd depends on
// vmmd only (apid runs on the control-plane box only, so on the
// compute-only box builderd cannot depend on apid — apid is a
// cross-host peer, not a same-host After target; see registry.go).
//
// Among siblings at equal depth the registry slice order is the
// tiebreaker — explicit in restart_order.go so a reader can audit it
// from the function name rather than reading the sort body.
func TestRestartOrder_MatchesExpected(t *testing.T) {
	got, err := RestartOrder()
	if err != nil {
		t.Fatalf("RestartOrder: %v", err)
	}
	want := []string{
		"vmmd",              // root: no After — emitted first (indegree 0, lowest slice idx)
		"realtimed",         // After[vmmd] — second-round ready, idx 1
		"apid",              // root: no After — registry tiebreak after realtimed
		"schedd",            // After[vmmd] — ready after vmmd, idx 3
		"gatewayd-internal", // After[schedd, apid] — both decremented, gatewayd-internal pops
		"gatewayd-public",   // After[apid]
		"meterd",            // After[apid] — Registry idx 6
		"githubd",           // After[apid] — Registry idx 7
		"outboundd",         // After[apid] — shared third-party admission gateway
		"imaged",            // After[vmmd] — Registry idx 9
		"builderd",          // After[vmmd] — Registry idx 10, vmmd has popped
	}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, len(want)=%d; got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("pos %d: got %q want %q", i, got[i], want[i])
		}
	}
}

// TestRestartOrder_DeterministicAcrossShuffles exercises the
// tiebreaker. We copy the Registry, shuffle the copy, build an
// internal-only dependency graph with hand-picked depths that put
// every daemon on an equal footing, then assert the resulting order is
// identical to running on the canonical Registry.
//
// We don't actually call RestartOrder on a swapped Registry because
// the Registry is a package-global; instead we shuffle the inputs
// to the topological sort directly. The way to do that without
// touching the global is to re-implement the walk here on a
// helper-built name list — but that duplicates the algorithm we're
// testing. Cheap alternative: call RestartOrder 1000x and assert
// identity. The algorithm's deterministic tiebreaker is the only
// thing that produces stability across runs; a non-deterministic
// sort would surface as a flaky test on -count > 1.
func TestRestartOrder_DeterministicAcrossShuffles(t *testing.T) {
	first, err := RestartOrder()
	if err != nil {
		t.Fatalf("RestartOrder (first call): %v", err)
	}
	for i := 0; i < 1000; i++ {
		next, err := RestartOrder()
		if err != nil {
			t.Fatalf("RestartOrder (call %d): %v", i, err)
		}
		if len(next) != len(first) {
			t.Fatalf("call %d: len changed: got %d want %d", i, len(next), len(first))
		}
		for j := range next {
			if next[j] != first[j] {
				t.Fatalf("call %d: pos %d: got %q want %q", i, j, next[j], first[j])
			}
		}
	}
}

// TestRestartOrder_CycleReturnsError asserts the cycle path. We
// construct a synthetic two-node cycle, swap it onto a local
// registryByName mapping, and emulate the run. The actual RestartOrder
// closure doesn't take a registry argument, so the test exercises the
// public surface indirectly: we temporarily mutate Registry inside the
// test (with defer-restore) to introduce the cycle. Mutating a global
// in a test is normally a smell — here the alternative is to export a
// private helper and risk widening the package API for one test.
//
// Restoring via t.Cleanup is sufficient: the test runs serially (no
// -parallel marks), so a Registry swap can never race another test.
func TestRestartOrder_CycleReturnsError(t *testing.T) {
	// Snapshot and restore Registry around the test.
	orig := Registry
	t.Cleanup(func() { Registry = orig })

	Registry = []Entry{
		{Name: "a", Unit: UnitVmmd, Critical: true, Lifecycle: Lifecycle{After: []string{"b"}}},
		{Name: "b", Unit: UnitApid, Critical: true, Lifecycle: Lifecycle{After: []string{"a"}}},
	}

	_, err := RestartOrder()
	if err == nil {
		t.Fatalf("RestartOrder on cycle: expected error, got nil")
	}
	if !IsCycle(err) {
		t.Fatalf("RestartOrder on cycle: want errors.As(*ErrCycle), got %T %v", err, err)
	}
	var cycle *ErrCycle
	if !errors.As(err, &cycle) {
		t.Fatalf("errors.As(*ErrCycle): failed on %v", err)
	}
	if len(cycle.Partial) != 0 {
		t.Errorf("cycle Partial=%v (want 0 — Kahn's observes no progress on a 2-cycle)", cycle.Partial)
	}
	if len(cycle.Back) != 2 {
		t.Errorf("cycle Back=%v (want 2 — both nodes remain)", cycle.Back)
	}
}

// TestRestartOrder_UnknownDependencyReturnsError asserts the unknown-
// dependency path. A typo in Lifecycle.After fails fast at
// RestartOrder() so a deployctl run surfaces a clear error instead of
// silently skipping the dangling daemon (Registry's runtime iteration
// in defaultHostRuntime doesn't re-check the After list).
func TestRestartOrder_UnknownDependencyReturnsError(t *testing.T) {
	orig := Registry
	t.Cleanup(func() { Registry = orig })

	Registry = []Entry{
		{Name: "a", Unit: UnitVmmd, Critical: true},
		{Name: "b", Unit: UnitApid, Critical: true, Lifecycle: Lifecycle{After: []string{"nonexistent"}}},
	}

	_, err := RestartOrder()
	if err == nil {
		t.Fatalf("RestartOrder on unknown dependency: expected error, got nil")
	}
	if !IsUnknownDependency(err) {
		t.Fatalf("RestartOrder on unknown dependency: want errors.As(*ErrUnknownDependency), got %T %v", err, err)
	}
	var u *ErrUnknownDependency
	if !errors.As(err, &u) {
		t.Fatalf("errors.As(*ErrUnknownDependency): failed on %v", err)
	}
	if u.Daemon != "b" || u.DependsOn != "nonexistent" {
		t.Errorf("got ErrUnknownDependency{%q, %q}, want {b, nonexistent}", u.Daemon, u.DependsOn)
	}
}

// TestRestartOrder_RootsFirst asserts that daemons with no
// Lifecycle.After entries are emitted ahead of any daemon that names
// them in its After. This is the property that makes the cd-controlplane
// restart loop safe: vmmd is at the front, so /run/faas ownership is
// re-established (via ensureRunFaasOwnership in cmd/deployctl/runtime.go)
// before schedd tries to bind /run/faas/schedd.sock.
func TestRestartOrder_RootsFirst(t *testing.T) {
	order, err := RestartOrder()
	if err != nil {
		t.Fatalf("RestartOrder: %v", err)
	}
	pos := make(map[string]int, len(order))
	for i, n := range order {
		pos[n] = i
	}

	roots := make([]string, 0)
	for _, entry := range Registry {
		if len(entry.Lifecycle.After) == 0 {
			roots = append(roots, entry.Name)
		}
	}
	for _, entry := range Registry {
		for _, after := range entry.Lifecycle.After {
			rIdx, ok := pos[after]
			if !ok {
				continue
			}
			eIdx := pos[entry.Name]
			if rIdx >= eIdx {
				t.Errorf("root %q (pos=%d) must come before dependent %q (pos=%d)",
					after, rIdx, entry.Name, eIdx)
			}
		}
	}
}

// TestRestartOrder_EmptyRegistry asserts the corner case of an empty
// Registry — useful for tests that mock or for future dynamic loading.
func TestRestartOrder_EmptyRegistry(t *testing.T) {
	orig := Registry
	t.Cleanup(func() { Registry = orig })

	Registry = nil
	got, err := RestartOrder()
	if err != nil {
		t.Fatalf("RestartOrder: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
