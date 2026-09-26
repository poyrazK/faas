// adr: 137
// recovery_arbiter_test.go — table-driven coverage for the
// recovery arbiter's per-(node, instance) verdict (Workstream B
// / issue #1184 / ADR-137).
//
// The arbiter is a pure function: same inputs → same output.
// The table below pins the closed decision matrix documented
// in recovery_arbiter.go's Decide doc-comment. A future
// SnapshotReplication column (Task #64) will add a 9th row
// (running on unavailable with no usable snapshot → Recreate);
// adding a row here is the load-bearing step when the column
// lands.
package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// noopDispatcher satisfies MigrationDispatcher + RecreateDispatcher
// with no-op Enqueue / RecreateInstance. The pure-Decide tests
// below don't exercise dispatch — the dispatcher counters land
// in TestArbiter_Tick_DispatchCounts instead.
type noopDispatcher struct {
	mu            sync.Mutex // Tick dispatches a node's migrations concurrently
	enqueueCalls  []string
	recreateCalls []string
}

func (n *noopDispatcher) Enqueue(_ context.Context, id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enqueueCalls = append(n.enqueueCalls, id)
	return nil
}
func (n *noopDispatcher) RecreateInstance(_ context.Context, id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recreateCalls = append(n.recreateCalls, id)
	return nil
}

// TestArbiter_Decide_Table pins the decision matrix from ADR-137,
// including healthy boot states and the in-flight guards added by fix #6.
// Each row is a (node.lifecycle, instance.state) pair → expected
// verdict. The in-flight rows (`migrating`, `snapshotting`,
// `evicting_*`) pin the deny-list introduced so the arbiter
// doesn't race a peer primitive (live_migrator, snapshot_reaper,
// eviction sweep) that already owns the row.
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
			name:     "draining_job_task → None (lease reaper owns retry)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "running", Kind: "job_task"},
			want:     DecisionNone,
		},
		{
			name:     "draining_waking → None (healthy boot stays local)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "waking"},
			want:     DecisionNone,
		},
		{
			name:     "draining_cold_booting → None (healthy boot stays local)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "cold_booting"},
			want:     DecisionNone,
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
			name:     "recovering_waking → None (healthy boot stays local)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleRecovering},
			instance: state.RecoveryInstance{State: "waking"},
			want:     DecisionNone,
		},
		{
			name:     "recovering_cold_booting → None (healthy boot stays local)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleRecovering},
			instance: state.RecoveryInstance{State: "cold_booting"},
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
		// Fix #6: in-flight deny-list. The arbiter must NOT
		// re-issue a verdict for a row whose state says a peer
		// primitive already owns it — that would race the
		// in-flight transition (a second LiveMigrate enqueue,
		// a second recreate on the same row, etc).
		{
			name:     "unavailable_migrating → None (in-flight)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable},
			instance: state.RecoveryInstance{State: "migrating"},
			want:     DecisionNone,
		},
		{
			name:     "draining_snapshotting → None (in-flight)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleDraining},
			instance: state.RecoveryInstance{State: "snapshotting"},
			want:     DecisionNone,
		},
		{
			name:     "unavailable_evicting_grace → None (in-flight)",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable},
			instance: state.RecoveryInstance{State: "evicting_grace"},
			want:     DecisionNone,
		},
		{
			name:     "force-draining waking is recreated",
			node:     state.ComputeNode{Lifecycle: state.NodeLifecycleForceDraining},
			instance: state.RecoveryInstance{State: "waking"},
			want:     DecisionRecreate,
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

// TestArbiter_Tick_UnhealthyMigrationFailureFallsBackToRecreate pins the
// failure-safe landing for a running row whose source node cannot complete a
// live handoff. A healthy drain must keep retry semantics, while unavailable
// and force-draining sources can safely park the row for service convergence.
func TestArbiter_Tick_UnhealthyMigrationFailureFallsBackToRecreate(t *testing.T) {
	t.Parallel()
	for _, lifecycle := range []state.NodeLifecycle{
		state.NodeLifecycleUnavailable,
		state.NodeLifecycleForceDraining,
	} {
		lifecycle := lifecycle
		t.Run(string(lifecycle), func(t *testing.T) {
			t.Parallel()
			recreate := &noopDispatcher{}
			migrationErr := errors.New("source vmmd unavailable")
			a := NewArbiter(MigrationDispatcherFunc(func(context.Context, string) error {
				return migrationErr
			}), recreate)

			liveMig, recreated, skipped, err := a.Tick(context.Background(),
				[]state.ComputeNode{{ID: "n1", Lifecycle: lifecycle}},
				map[string][]state.RecoveryInstance{
					"n1": {{ID: "i1", State: string(state.StateRunning)}},
				})
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if liveMig != 0 || recreated != 1 || skipped != 0 {
				t.Fatalf("counts = (%d, %d, %d), want (0, 1, 0)", liveMig, recreated, skipped)
			}
			if len(recreate.recreateCalls) != 1 || recreate.recreateCalls[0] != "i1" {
				t.Fatalf("recreate calls = %v, want [i1]", recreate.recreateCalls)
			}
		})
	}
}

func TestArbiter_Tick_HealthyMigrationFailureRetries(t *testing.T) {
	t.Parallel()
	for _, lifecycle := range []state.NodeLifecycle{
		state.NodeLifecycleDraining,
		state.NodeLifecycleRecovering,
	} {
		lifecycle := lifecycle
		t.Run(string(lifecycle), func(t *testing.T) {
			t.Parallel()
			recreate := &noopDispatcher{}
			migrationErr := errors.New("temporary migration failure")
			a := NewArbiter(MigrationDispatcherFunc(func(context.Context, string) error {
				return migrationErr
			}), recreate)

			liveMig, recreated, skipped, err := a.Tick(context.Background(),
				[]state.ComputeNode{{ID: "n1", Lifecycle: lifecycle}},
				map[string][]state.RecoveryInstance{
					"n1": {{ID: "i1", State: string(state.StateRunning)}},
				})
			if !errors.Is(err, migrationErr) {
				t.Fatalf("Tick error = %v, want migration error", err)
			}
			if liveMig != 0 || recreated != 0 || skipped != 0 {
				t.Fatalf("counts = (%d, %d, %d), want (0, 0, 0)", liveMig, recreated, skipped)
			}
			if len(recreate.recreateCalls) != 0 {
				t.Fatalf("recreate calls = %v, want none", recreate.recreateCalls)
			}
		})
	}
}

func TestArbiter_Tick_ConflictDoesNotFallback(t *testing.T) {
	t.Parallel()
	recreate := &noopDispatcher{}
	a := NewArbiter(MigrationDispatcherFunc(func(context.Context, string) error {
		return state.ErrConflict
	}), recreate)

	_, recreated, _, err := a.Tick(context.Background(),
		[]state.ComputeNode{{ID: "n1", Lifecycle: state.NodeLifecycleUnavailable}},
		map[string][]state.RecoveryInstance{
			"n1": {{ID: "i1", State: string(state.StateRunning)}},
		})
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("Tick error = %v, want ErrConflict", err)
	}
	if recreated != 0 {
		t.Fatalf("recreated = %d, want 0", recreated)
	}
	if len(recreate.recreateCalls) != 0 {
		t.Fatalf("recreate calls = %v, want none", recreate.recreateCalls)
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

// gatedDispatcher holds every live migration until release closes, so a test
// can observe how many Tick runs at once.
type gatedDispatcher struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	calls       []string
	release     chan struct{}
}

func (g *gatedDispatcher) Enqueue(_ context.Context, id string) error {
	g.mu.Lock()
	g.inFlight++
	if g.inFlight > g.maxInFlight {
		g.maxInFlight = g.inFlight
	}
	g.calls = append(g.calls, id)
	g.mu.Unlock()
	<-g.release
	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return nil
}

func (g *gatedDispatcher) running() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}

func runningInstances(n int) []state.RecoveryInstance {
	out := make([]state.RecoveryInstance, n)
	for i := range out {
		out[i] = state.RecoveryInstance{ID: fmt.Sprintf("i%d", i), State: string(state.StateRunning)}
	}
	return out
}

// A draining node's live migrations run concurrently, bounded by the
// configured limit: each handoff takes ~85 s, so dispatching them one by one
// made a drain last that long per running instance.
func TestArbiter_Tick_MigratesNodeConcurrentlyWithinLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ limit, instances int }{{1, 3}, {2, 5}, {4, 6}, {4, 2}} {
		t.Run(fmt.Sprintf("limit=%d/instances=%d", tc.limit, tc.instances), func(t *testing.T) {
			t.Parallel()
			gate := &gatedDispatcher{release: make(chan struct{})}
			a := NewArbiter(gate, &noopDispatcher{}).WithLiveMigrationConcurrency(tc.limit)
			type result struct {
				liveMig int
				err     error
			}
			done := make(chan result, 1)
			go func() {
				liveMig, _, _, err := a.Tick(context.Background(),
					[]state.ComputeNode{{ID: "n1", Lifecycle: state.NodeLifecycleDraining}},
					map[string][]state.RecoveryInstance{"n1": runningInstances(tc.instances)})
				done <- result{liveMig, err}
			}()
			want := min(tc.limit, tc.instances)
			deadline := time.Now().Add(5 * time.Second)
			for gate.running() < want {
				if time.Now().After(deadline) {
					t.Fatalf("only %d migrations started, want %d at once", gate.running(), want)
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(20 * time.Millisecond) // give an over-limit dispatch time to show up
			if got := gate.running(); got != want {
				t.Fatalf("%d migrations in flight, want exactly %d", got, want)
			}
			close(gate.release)
			res := <-done
			if res.err != nil || res.liveMig != tc.instances {
				t.Fatalf("Tick = (%d, %v), want (%d, nil)", res.liveMig, res.err, tc.instances)
			}
			if gate.maxInFlight != want {
				t.Fatalf("max in flight = %d, want %d", gate.maxInFlight, want)
			}
			sort.Strings(gate.calls)
			for i, id := range gate.calls {
				if id != fmt.Sprintf("i%d", i) {
					t.Fatalf("migrated %v, want every instance once", gate.calls)
				}
			}
		})
	}
}

// Concurrent dispatch keeps the per-instance outcomes: every failure is
// reported, and an unhealthy source still recreates each failed handoff.
func TestArbiter_Tick_ConcurrentOutcomes(t *testing.T) {
	t.Parallel()
	errA, errB := errors.New("handoff a failed"), errors.New("handoff b failed")
	fail := MigrationDispatcherFunc(func(_ context.Context, id string) error {
		switch id {
		case "i0":
			return errA
		case "i1":
			return errB
		}
		return nil
	})
	_, _, _, err := NewArbiter(fail, &noopDispatcher{}).Tick(context.Background(),
		[]state.ComputeNode{{ID: "n1", Lifecycle: state.NodeLifecycleDraining}},
		map[string][]state.RecoveryInstance{"n1": runningInstances(4)})
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("Tick error = %v, want both handoff failures", err)
	}

	recreate := &noopDispatcher{}
	liveMig, recreated, _, err := NewArbiter(fail, recreate).Tick(context.Background(),
		[]state.ComputeNode{{ID: "n1", Lifecycle: state.NodeLifecycleUnavailable}},
		map[string][]state.RecoveryInstance{"n1": runningInstances(4)})
	if err != nil || liveMig != 2 || recreated != 2 {
		t.Fatalf("unavailable source: (%d migrated, %d recreated, %v), want (2, 2, nil)", liveMig, recreated, err)
	}
	sort.Strings(recreate.recreateCalls)
	if fmt.Sprint(recreate.recreateCalls) != "[i0 i1]" {
		t.Fatalf("recreated %v, want [i0 i1]", recreate.recreateCalls)
	}
}
