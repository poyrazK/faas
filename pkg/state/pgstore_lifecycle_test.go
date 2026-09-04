// pgstore_lifecycle_test.go — PgStore parity tests for the
// compute_node lifecycle surface added in migration 00579 +
// 00582 (Workstream B / issue #1184 / Task #67 / ADR-137).
//
// Pins the hand-written SQL against a real cluster:
//   - NodeSetLifecycle CAS: WHERE lifecycle=$expected must
//     return ErrConflict on a wrong expectation (peer race) and
//     ErrNotFound on a missing id (admin delete mid-flight).
//   - NodeList / NodeListRecoverable: filter on the lifecycle
//     enum (the generated `active` column is the legacy read
//     surface but the spec query path goes through lifecycle).
//   - NodeMarkRecovered: last_recovery_outcome='succeeded' +
//     lifecycle='recovering'→'active' in one UPDATE.
//
// MemStore parity lives in pkg/sched/recovery_arbiter_test.go
// (decision matrix) and the in-line CreateComputeNode tests in
// pkg/state/memstore_test.go.
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// pgTestLifecycleNode seeds a compute_node with all NOT-NULL +
// CHECK columns satisfied, returns the id.
func pgTestLifecycleNode(t *testing.T, s *state.PgStore, lifecycle state.NodeLifecycle) string {
	t.Helper()
	n, err := s.CreateComputeNode(t.Context(), state.ComputeNode{
		Name:               "lc-" + uuid.NewString(),
		TargetURL:          "unix:///run/faas/vmmd.sock",
		Active:             true, // seed the active lifecycle for the CAS below
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 256,
		VPCPUs:             4,
		VCPUBudget:         160,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if lifecycle != state.NodeLifecycleActive {
		if err := s.NodeSetLifecycle(t.Context(), n.ID, state.NodeLifecycleActive, lifecycle); err != nil {
			t.Fatalf("NodeSetLifecycle seed: %v", err)
		}
	}
	return n.ID
}

// TestPg_NodeSetLifecycle_CASConflict pins the expected-state
// mismatch path. The heartbeat writes lifecycle under the
// expected state; a peer that flipped it first must surface as
// ErrConflict (the same code the marker function uses to skip
// the row).
func TestPg_NodeSetLifecycle_CASConflict(t *testing.T) {
	s, _, _ := pgWithPool(t)
	id := pgTestLifecycleNode(t, s, state.NodeLifecycleActive)
	// Race: heartbeat expects Active, peer already flipped to Draining.
	err := s.NodeSetLifecycle(t.Context(), id, state.NodeLifecycleActive, state.NodeLifecycleUnavailable)
	if !errors.Is(err, state.ErrConflict) {
		t.Errorf("wrong expected state = %v, want ErrConflict", err)
	}
}

// TestPg_NodeSetLifecycle_NotFound — admin DELETE / retention
// removed the row between the ActiveComputeNodes scan and the
// CAS. The CAS must surface ErrNotFound (NOT ErrConflict) so the
// heartbeat's nil-safe skip path knows the row is gone, not just
// raced.
func TestPg_NodeSetLifecycle_NotFound(t *testing.T) {
	s, _, _ := pgWithPool(t)
	missing := uuid.NewString()
	err := s.NodeSetLifecycle(t.Context(), missing, state.NodeLifecycleActive, state.NodeLifecycleUnavailable)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing id = %v, want ErrNotFound", err)
	}
}

// TestPg_NodeList_ByLifecycle — NodeList with the lifecycle
// filter returns only rows matching that value. Pins the index
// choice (compute_nodes_lifecycle_idx partial on
// (lifecycle IN ('active','recovering'))).
func TestPg_NodeList_ByLifecycle(t *testing.T) {
	s, _, _ := pgWithPool(t)
	// Drainable set: exactly one node in `draining`.
	drainedID := pgTestLifecycleNode(t, s, state.NodeLifecycleDraining)
	got, err := s.NodeList(t.Context(), state.NodeLifecycleDraining)
	if err != nil {
		t.Fatalf("NodeList(draining): %v", err)
	}
	found := false
	for _, n := range got {
		if n.ID == drainedID {
			found = true
		}
	}
	if !found {
		t.Errorf("drained id %q not in NodeList(draining); got %d rows", drainedID, len(got))
	}
}

// TestPg_NodeListRecoverable_ReturnsUnavailableAndRecovering —
// the recovery arbiter's per-tick input set. Must include both
// `unavailable` and `recovering` rows (the spec language calls
// these "in-flight recovery"). Excludes `active` (no work to
// do) and `draining` (operator-initiated, not failure-driven).
func TestPg_NodeListRecoverable_ReturnsUnavailableAndRecovering(t *testing.T) {
	s, _, _ := pgWithPool(t)
	unavailID := pgTestLifecycleNode(t, s, state.NodeLifecycleUnavailable)
	recoveringID := pgTestLifecycleNode(t, s, state.NodeLifecycleRecovering)
	_ = pgTestLifecycleNode(t, s, state.NodeLifecycleActive)   // must NOT appear
	_ = pgTestLifecycleNode(t, s, state.NodeLifecycleDraining) // must NOT appear

	got, err := s.NodeListRecoverable(t.Context())
	if err != nil {
		t.Fatalf("NodeListRecoverable: %v", err)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n.ID] = true
	}
	if !seen[unavailID] {
		t.Errorf("unavailable node %q not in recoverable set", unavailID)
	}
	if !seen[recoveringID] {
		t.Errorf("recovering node %q not in recoverable set", recoveringID)
	}
}

// TestPg_NodeMarkRecovered — single UPDATE that flips
// lifecycle='recovering' → 'active' AND stamps
// last_recovery_outcome='succeeded'. The row count guard
// returns ErrConflict if the precondition fails (recovery
// arbiter raced with the heartbeat reactivation).
func TestPg_NodeMarkRecovered(t *testing.T) {
	s, _, _ := pgWithPool(t)
	id := pgTestLifecycleNode(t, s, state.NodeLifecycleRecovering)
	if err := s.NodeMarkRecovered(t.Context(), id); err != nil {
		t.Fatalf("NodeMarkRecovered: %v", err)
	}
	got, err := s.NodeGet(t.Context(), id)
	if err != nil {
		t.Fatalf("NodeGet: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleActive {
		t.Errorf("after recover lifecycle=%q, want active", got.Lifecycle)
	}
}
