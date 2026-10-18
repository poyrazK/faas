// Operator-intent queue and node execution tests. Workload recovery protocols
// have separate journal, acknowledgement and dispatcher suites.
// adr: 174
// adr: 1526
// adr: 1528
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// An empty queue returns cleanly through the real loop and engine.
func TestDrainPendingOperatorIntents_NoRowsReturnsCleanly(t *testing.T) {
	loop := &Loop{engine: newEngine(t, state.NewMemStore(), &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	loop.drainPendingOperatorIntents(context.Background())
}

// Claim and atomic completion leave no terminal row eligible for redispatch.
func TestInsertClaimMark_Lifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	actor := "actor-id"
	acct := "acct-id"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		node.ID, &acct, actor, "test reason", json.RawMessage(`{}`), nil,
	)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.ID != id {
		t.Errorf("Claim id=%q, want %q", got.ID, id)
	}
	if got.Status != state.OperatorIntentRunning {
		t.Errorf("Claim status=%q, want running", got.Status)
	}

	if _, err := store.ExecuteOperatorNode(ctx, id, got.ClaimToken); err != nil {
		t.Fatalf("ExecuteOperatorNode: %v", err)
	}

	// Second claim: row is terminal, invisible to the claim query.
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("second claim after succeeded: got %v, want ErrOperatorIntentNotFound", err)
	}
}

func TestApplyNodeOperatorIntent_LifecycleTransitions(t *testing.T) {
	tests := []struct {
		name    string
		kind    state.OperatorIntentKind
		initial state.NodeLifecycle
		want    state.NodeLifecycle
	}{
		{name: "drain", kind: state.OperatorIntentKindNodeDrain, initial: state.NodeLifecycleActive, want: state.NodeLifecycleDraining},
		{name: "force drain", kind: state.OperatorIntentKindNodeForceDrain, initial: state.NodeLifecycleRecovering, want: state.NodeLifecycleForceDraining},
		{name: "activate", kind: state.OperatorIntentKindNodeActivate, initial: state.NodeLifecycleUnavailable, want: state.NodeLifecycleActive},
		{name: "activate maintenance", kind: state.OperatorIntentKindNodeActivate, initial: state.NodeLifecycleMaintenance, want: state.NodeLifecycleActive},
		{name: "retire", kind: state.OperatorIntentKindNodeRetire, initial: state.NodeLifecycleMaintenance, want: state.NodeLifecycleRetired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			node, err := store.CreateComputeNode(ctx, state.ComputeNode{
				Name:      "node-a",
				TargetURL: "unix:///run/faas/vmmd.sock",
				Lifecycle: tt.initial,
			})
			if err != nil {
				t.Fatalf("seed node: %v", err)
			}
			intent := state.OperatorIntent{Kind: tt.kind, TargetID: node.ID}
			if err := applyNodeOperatorIntentFixture(t, ctx, store, intent); err != nil {
				t.Fatalf("apply node intent: %v", err)
			}
			got, err := store.NodeGet(ctx, node.ID)
			if err != nil {
				t.Fatalf("read node: %v", err)
			}
			if got.Lifecycle != tt.want {
				t.Fatalf("lifecycle=%q, want %q", got.Lifecycle, tt.want)
			}
		})
	}
}

func TestApplyNodeOperatorIntent_RetireRechecksLiveInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-retire-race", TargetURL: "unix:///run/faas/vmmd.sock",
		Lifecycle: state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := store.CreateInstance(ctx, "app-a", "dep-a", string(state.StateRunning), 128, node.ID, "wake-a"); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if err := store.NodeSetLifecycle(ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleMaintenance); err != nil {
		t.Fatal(err)
	}
	err = applyNodeOperatorIntentFixture(t, ctx, store, state.OperatorIntent{Kind: state.OperatorIntentKindNodeRetire, TargetID: node.ID})
	if err == nil {
		t.Fatal("retirement unexpectedly ignored a live instance")
	}
	fresh, getErr := store.NodeGet(ctx, node.ID)
	if getErr != nil || fresh.Lifecycle != state.NodeLifecycleMaintenance {
		t.Fatalf("rejected retirement mutated node: node=%+v err=%v", fresh, getErr)
	}
}

func TestApplyNodeOperatorIntent_ActivateRefusesControllerOwnedState(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "node-draining",
		TargetURL: "unix:///run/faas/vmmd.sock",
		Lifecycle: state.NodeLifecycleDraining,
	})
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	err = applyNodeOperatorIntentFixture(t, ctx, store, state.OperatorIntent{
		Kind:     state.OperatorIntentKindNodeActivate,
		TargetID: node.ID,
	})
	if err == nil {
		t.Fatal("activate unexpectedly cancelled a controller-owned drain")
	}
	got, getErr := store.NodeGet(ctx, node.ID)
	if getErr != nil {
		t.Fatalf("read node: %v", getErr)
	}
	if got.Lifecycle != state.NodeLifecycleDraining {
		t.Fatalf("rejected activate mutated lifecycle=%q", got.Lifecycle)
	}
}

// TestFIFOClaimOrdering pins the FIFO invariant: two pending
// rows are claimed in requested_at ASC order. A regression
// here would let a slow row overtake a fast one — not a
// correctness violation today (each row is processed
// independently) but a future ordering hazard for audit-log
// replay.
func TestFIFOClaimOrdering(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	actor := "actor-id"
	acct := "acct-id"

	first, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		"first-target", &acct, actor, "first", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert first: %v", err)
	}
	second, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForceColdBoot,
		"second-target", &acct, actor, "second", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert second: %v", err)
	}

	// Claim 1 → first (older). Claim 2 → second (newer).
	got1, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 1: %v", err)
	}
	if got1.ID != first {
		t.Errorf("Claim 1 id=%q, want first=%q", got1.ID, first)
	}
	got2, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if got2.ID != second {
		t.Errorf("Claim 2 id=%q, want second=%q", got2.ID, second)
	}

	// Complete both through their owning transactional executors.
	if _, err := store.ExecuteOperatorNode(ctx, first, got1.ClaimToken); err != nil {
		t.Fatalf("ExecuteOperatorNode first: %v", err)
	}
	if _, err := store.ExecuteOperatorColdBoot(ctx, second, got2.ClaimToken); err != nil {
		t.Fatalf("ExecuteOperatorColdBoot second: %v", err)
	}

	// Third claim → empty queue.
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("third claim: got %v, want ErrOperatorIntentNotFound", err)
	}
}

// A durable failed result is terminal and is never claimed for execution again.
func TestMarkFailedLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	actor := "actor-id"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		"target-id", nil, actor, "test", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	intent, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecuteOperatorNode(ctx, id, intent.ClaimToken); err != nil {
		t.Fatalf("ExecuteOperatorNode failed outcome: %v", err)
	}

	got, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != state.OperatorIntentFailed {
		t.Errorf("status=%q, want failed", got.Status)
	}
	if got.Error != "Compute node is unavailable." {
		t.Errorf("error=%q", got.Error)
	}
	if got.FinishedAt == nil {
		t.Errorf("finished_at not stamped")
	}

	// Second claim: terminal row invisible.
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("claim after failed: got %v, want ErrOperatorIntentNotFound", err)
	}
}

// Unfenced setters cannot complete a node intent owned by a current worker.
func TestUnfencedOperatorNodeCompletionRejected(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	id, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindNodeDrain, "target-id", nil, "actor-id", "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOperatorIntentSucceeded(ctx, id, nil); err == nil {
		t.Fatal("unfenced completion accepted")
	}
	row, err := store.GetOperatorIntent(ctx, id)
	if err != nil || row.Status != state.OperatorIntentRunning {
		t.Fatalf("unexpected outcome: %+v %v", row, err)
	}
}

// TestForceRestart_FIFOOrderingWithExistingKinds pins that the
// FIFO claim query orders force_restart alongside the existing
// two kinds by requested_at ASC. A regression here (e.g. a
// default-case filters out force_restart) would surface as an
// audit-log replay hazard — a force_restart row gets stuck
// behind a force_park row that's dispatched first.
//
// Mirrors TestFIFOClaimOrdering shape: three rows in
// force_park → force_cold_boot → force_restart order, all
// should claim in that order.
func TestForceRestart_FIFOOrderingWithExistingKinds(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	actor := "actor-id"
	acct := "acct-id"

	park, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"park-target", &acct, actor, "park", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert park: %v", err)
	}
	coldboot, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForceColdBoot,
		"coldboot-target", &acct, actor, "coldboot", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert coldboot: %v", err)
	}
	restart, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForceRestart,
		"restart-target", &acct, actor, "restart", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert restart: %v", err)
	}

	// Claim in insertion order.
	g1, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 1: %v", err)
	}
	if g1.ID != park {
		t.Errorf("Claim 1 id=%q, want park=%q", g1.ID, park)
	}
	g2, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if g2.ID != coldboot {
		t.Errorf("Claim 2 id=%q, want coldboot=%q", g2.ID, coldboot)
	}
	g3, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 3: %v", err)
	}
	if g3.ID != restart {
		t.Errorf("Claim 3 id=%q, want restart=%q (force_restart must not be filtered by the claim query)", g3.ID, restart)
	}

	// Fourth claim → empty queue.
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("fourth claim: got %v, want ErrOperatorIntentNotFound", err)
	}
}

// Exercise the same durable execution path used by the dispatcher.
func applyNodeOperatorIntentFixture(t *testing.T, ctx context.Context, store *state.MemStore, input state.OperatorIntent) error {
	t.Helper()
	id, err := store.InsertOperatorIntent(ctx, input.Kind, input.TargetID, nil, "actor-id", "test", nil, nil)
	if err != nil {
		return err
	}
	intent, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		return err
	}
	result, err := store.ExecuteOperatorNode(ctx, id, intent.ClaimToken)
	if err != nil {
		return err
	}
	if result.Status == state.OperatorIntentFailed {
		return fmt.Errorf("node intent failed: %s", result.Error)
	}
	return nil
}
