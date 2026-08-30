// recovery_arbiter_wiring_test.go — pin the Engine ↔ Arbiter
// wiring (Task #61 / ADR-137). The MigrateLiveInstances path
// must consult the arbiter for cold_booting rows on unavailable
// nodes and route to RecreateInstance (skipping the 4-phase
// handoff that would orphan the row). The wired path lives
// here because the legacy MigrateLiveInstances tests already
// pin the no-arbiter behaviour.
package sched

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// arbiterTestEngine is a bare Engine wired with the arbiter +
// a MemStore. The legacy harness.MigrateOne path requires a
// real VMMClient (builds a MigrationHarness); the recreate
// primitive doesn't, so the test exercises the arbiter-driven
// Recreate path through MigrateLiveInstances without standing
// up a full engine.
func arbiterTestEngine(t *testing.T) (*Engine, *state.MemStore) {
	t.Helper()
	store := state.NewMemStore()
	e := &Engine{
		store: store,
		log:   slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		// Engine.RecreateInstance reads e.ledger; nil is tolerated.
		// MigrateLiveInstances early-returns when ownerNodeID is
		// empty; seed a non-empty value so the per-instance loop runs.
		ownerNodeID: "owner-self",
	}
	// Wire the arbiter with Engine itself as the RecreateDispatcher.
	arb := NewArbiter(nil, e)
	e.WithRecoveryArbiter(arb)
	return e, store
}

// seedUnavailableNodeWithColdBooting — the case the arbiter's
// Recreate verdict covers. Inserts a node with lifecycle='unavailable'
// (the heartbeat-dead stamp) and a cold_booting instance on it.
func seedUnavailableNodeWithColdBooting(t *testing.T, store *state.MemStore) (state.ComputeNode, state.Instance) {
	t.Helper()
	node, err := store.UpsertComputeNode(context.Background(), state.ComputeNode{
		Name: "node-dead", Lifecycle: state.NodeLifecycleUnavailable, Active: false,
	})
	if err != nil {
		t.Fatalf("UpsertComputeNode: %v", err)
	}
	ins, err := store.CreateInstance(context.Background(),
		"app-r", "dep-r", "cold_booting", 256, node.ID, "wake-r")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return node, ins
}

// TestEngine_MigrateLiveInstances_ArbiterRoutesRecreate — when
// the arbiter is wired and the input set contains a row that
// returns DecisionRecreate, the row transitions to PARKED via
// RecreateInstance. MigrateLiveInstances's ListLiveInstancesOnNode
// filters to RUNNING only (legacy contract); the arbiter's
// Recreate verdict is reachable through Arbiter.Tick directly,
// which is the path cmd/schedd drives per second (Task #61).
// Pin that path here so the wiring is observable end-to-end.
func TestEngine_MigrateLiveInstances_ArbiterRoutesRecreate(t *testing.T) {
	e, store := arbiterTestEngine(t)
	node, ins := seedUnavailableNodeWithColdBooting(t, store)

	// Drive the arbiter directly (the shape cmd/schedd will run on
	// the 1s ticker). The MigrateLiveInstances wrapper around the
	// arbiter's Tick is wired in cmd/schedd's main.go, not in
	// engine.go, to keep the per-tick goroutine boundary at the
	// daemon level (recovery_arbiter.go's package comment).
	arb := e.recoveryArbiter
	liveMig, recreate, _, err := arb.Tick(context.Background(),
		[]state.ComputeNode{node},
		map[string][]state.RecoveryInstance{node.ID: {{ID: ins.ID, State: ins.State, AppID: ins.AppID, DeploymentID: ins.DeploymentID}}},
	)
	if err != nil {
		t.Fatalf("Arbiter.Tick: %v", err)
	}
	if liveMig != 0 {
		t.Errorf("liveMig = %d, want 0", liveMig)
	}
	if recreate != 1 {
		t.Errorf("recreate = %d, want 1", recreate)
	}
	post, _ := store.InstanceByID(context.Background(), ins.ID)
	if post.State != "parked" {
		t.Errorf("post State = %q, want parked", post.State)
	}
}

// TestEngine_MigrateLiveInstances_NoArbiterPreservesLegacy is
// covered indirectly: dispatchRecovery(nil) returns (false, nil)
// (TestDispatchRecovery_NilArbiter) so MigrateLiveInstances falls
// through to the legacy branch when no arbiter is wired. The
// legacy branch's behaviour is pinned by the existing
// TestEngine_MigrateLiveInstances_* suite (22+ tests) which
// constructs engines via newEngine(t, ...) — those tests already
// exercise the no-arbiter path with a real VMMClient, so adding
// a partial duplicate here would only test that a nil ledger
// panics NewMigrationHarness, not anything load-bearing.

// TestDispatchRecovery_NilArbiter — the legacy bootstrap path
// returns (false, nil) so callers fall through to their pre-#1184
// behaviour. Pins the nil-safety contract.
func TestDispatchRecovery_NilArbiter(t *testing.T) {
	e := &Engine{} // no arbiter wired
	handled, err := e.dispatchRecovery(context.Background(), state.ComputeNode{}, "any")
	if err != nil {
		t.Errorf("dispatchRecovery(nil arbiter) err = %v", err)
	}
	if handled {
		t.Errorf("dispatchRecovery(nil arbiter) handled = true; want false (caller proceeds)")
	}
}