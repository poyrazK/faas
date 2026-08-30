// recovery_arbiter_test.go — table-driven coverage for the
// recovery arbiter's per-(node, instance) verdict (Workstream B
// / issue #1184 / ADR-137).
//
// The arbiter is a pure function: same inputs → same output.
// The 8 cases below pin the closed decision matrix documented
// in recovery_arbiter.go's Decide doc-comment. A future
// SnapshotReplication column (Task #64) will add a 9th row
// (running on unavailable with no usable snapshot → Recreate);
// adding a row here is the load-bearing step when the column
// lands.
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// noopDispatcher satisfies MigrationDispatcher + RecreateDispatcher
// with no-op Enqueue / RecreateInstance. The pure-Decide tests
// below don't exercise dispatch — the dispatcher counters land
// in TestArbiter_Tick_DispatchCounts instead.
type noopDispatcher struct {
	enqueueCalls  []string
	recreateCalls []string
}

func (n *noopDispatcher) Enqueue(_ context.Context, id string) error {
	n.enqueueCalls = append(n.enqueueCalls, id)
	return nil
}
func (n *noopDispatcher) RecreateInstance(_ context.Context, id string) error {
	n.recreateCalls = append(n.recreateCalls, id)
	return nil
}

// TestArbiter_Decide_Table pins the 8-case decision matrix from
// ADR-137. Each row is a (node.lifecycle, instance.state) pair
// → expected verdict.
func TestArbiter_Decide_Table(t *testing.T) {
	t.Parallel()
	a := NewArbiter(nil, nil) // dispatch not exercised in pure-Decide tests
	cases := []struct {
		name     string
		node     state.ComputeNode
		instance state.RecoveryInstance
		want     Decision
	}{
		{
			name:     "draining_running → LiveMigrate",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "running"},
			want:     DecisionLiveMigrate,
		},
		{
			name:     "draining_waking → LiveMigrate",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "waking"},
			want:     DecisionLiveMigrate,
		},
		{
			name:     "draining_parked → None",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "parked"},
			want:     DecisionNone,
		},
		{
			name:     "recovering_running → LiveMigrate",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleRecovering},
			instance: state.RecoveryInstance{State: "running"},
			want:     DecisionLiveMigrate,
		},
		{
			name:     "recovering_parked → None",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleRecovering},
			instance: state.RecoveryInstance{State: "parked"},
			want:     DecisionNone,
		},
		{
			name:     "unavailable_running → LiveMigrate",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable},
			instance: state.RecoveryInstance{State: "running"},
			want:     DecisionLiveMigrate,
		},
		{
			name:     "unavailable_cold_booting → Recreate",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable},
			instance: state.RecoveryInstance{State: "cold_booting"},
			want:     DecisionRecreate,
		},
		{
			name:     "active_running → None (out of scope)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleActive},
			instance: state.RecoveryInstance{State: "running"},
			want:     DecisionNone,
		},
		{
			name:     "unavailable_failed → None",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable},
			instance: state.RecoveryInstance{State: "failed"},
			want:     DecisionNone,
		},
		{
			name:     "unavailable_terminated → None",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable},
			instance: state.RecoveryInstance{State: "terminated"},
			want:     DecisionNone,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := a.Decide(tc.node, tc.instance)
			if got != tc.want {
				t.Errorf("Decide = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestArbiter_Tick_DispatchCounts — the Tick path drives both
// dispatchers and returns counts. 2 nodes × 2 instances each
// across all four lifecycle+state combinations yields the
// expected (liveMig, recreate, skipped) tuple.
func TestArbiter_Tick_DispatchCounts(t *testing.T) {
	t.Parallel()
	disp := &noopDispatcher{}
	a := NewArbiter(disp, disp)

	nodes := []state.ComputeNode{
		{ID: "n-drain", Lifecycle: state.NodeLifecycleDraining},
		{ID: "n-unavail", Lifecycle: state.NodeLifecycleUnavailable},
		{ID: "n-active", Lifecycle: state.NodeLifecycleActive},
	}
	instancesByNode := map[string][]state.RecoveryInstance{
		"n-drain": {
			{ID: "i1", State: "running"},
			{ID: "i2", State: "parked"}, // → None
		},
		"n-unavail": {
			{ID: "i3", State: "running"},      // → LiveMigrate
			{ID: "i4", State: "cold_booting"}, // → Recreate
		},
		// n-active: skipped by Tick (out of recovery scope)
	}
	liveMig, recreate, skipped, err := a.Tick(context.Background(), nodes, instancesByNode)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if liveMig != 2 {
		t.Errorf("liveMig = %d, want 2 (i1 + i3)", liveMig)
	}
	if recreate != 1 {
		t.Errorf("recreate = %d, want 1 (i4)", recreate)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (i2 parked)", skipped)
	}
	// Dispatcher call counts match the verdicts.
	if got := len(disp.enqueueCalls); got != 2 {
		t.Errorf("Enqueue calls = %d, want 2", got)
	}
	if got := len(disp.recreateCalls); got != 1 {
		t.Errorf("RecreateInstance calls = %d, want 1", got)
	}
	// Active node is filtered by Tick, no dispatch.
	if disp.enqueueCalls[0] != "i1" && disp.enqueueCalls[1] != "i1" {
		t.Errorf("expected i1 in enqueueCalls; got %v", disp.enqueueCalls)
	}
}

// TestArbiter_Tick_NilDispatchers — both dispatchers nil;
// Tick still returns counts without panicking. This matches
// the cmd/schedd bootstrap window where the engine hasn't
// been wired yet.
func TestArbiter_Tick_NilDispatchers(t *testing.T) {
	t.Parallel()
	a := NewArbiter(nil, nil)
	nodes := []state.ComputeNode{
		{ID: "n1", Lifecycle: state.NodeLifecycleUnavailable},
	}
	instancesByNode := map[string][]state.RecoveryInstance{
		"n1": {{ID: "i1", State: "running"}, {ID: "i2", State: "cold_booting"}},
	}
	liveMig, recreate, _, err := a.Tick(context.Background(), nodes, instancesByNode)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if liveMig != 1 {
		t.Errorf("liveMig = %d, want 1", liveMig)
	}
	if recreate != 1 {
		t.Errorf("recreate = %d, want 1", recreate)
	}
}

// TestDecision_String — the String form is the dashboard's
// verdict label. Pin the closed set so a future Decision
// addition doesn't silently render as "none".
func TestDecision_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   Decision
		want string
	}{
		{DecisionNone, "none"},
		{DecisionLiveMigrate, "live-migrate"},
		{DecisionRecreate, "recreate"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}
