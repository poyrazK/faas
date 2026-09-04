package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type recoveryRecreateFunc func(context.Context, string) error

func (f recoveryRecreateFunc) RecreateInstance(ctx context.Context, id string) error {
	return f(ctx, id)
}

func TestRecoveryRunnerCompletesEmptyDrain(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-drain",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := store.NodeSetLifecycle(ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatalf("start drain: %v", err)
	}

	runner := NewRecoveryRunner(store, NewArbiter(nil, nil), nil, testLog())
	if err := runner.Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}

	got, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleActive || got.DrainCompletedAt == nil {
		t.Fatalf("node after empty drain = %+v, want active with completion timestamp", got)
	}
}

func TestRecoveryRunnerCompletesEmptyRecovery(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-recover",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := store.NodeSetLifecycle(ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleRecovering); err != nil {
		t.Fatalf("start recovery: %v", err)
	}

	runner := NewRecoveryRunner(store, NewArbiter(nil, nil), nil, testLog())
	if err := runner.Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}

	got, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleActive || got.LastRecoveryOutcome == nil || *got.LastRecoveryOutcome != "succeeded" {
		t.Fatalf("node after empty recovery = %+v, want active/succeeded", got)
	}
}

func TestRecoveryRunnerRecreatesUnavailableBootWithoutCompletingNode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-unavailable",
		Lifecycle: state.NodeLifecycleUnavailable,
		Active:    false,
	})
	if err != nil {
		t.Fatalf("create unavailable node: %v", err)
	}
	instance, err := store.CreateInstance(ctx, "app-recovery-runner", "dep-recovery-runner", string(state.StateColdBooting), 128, node.ID, "wake-recovery-runner")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	recreated := 0
	arbiter := NewArbiter(nil, recoveryRecreateFunc(func(ctx context.Context, id string) error {
		recreated++
		return store.UpdateInstanceStateIf(ctx, id, string(state.StateColdBooting), string(state.StateParked))
	}))

	runner := NewRecoveryRunner(store, arbiter, nil, testLog())
	if err := runner.Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if recreated != 1 {
		t.Fatalf("recreate calls = %d, want 1", recreated)
	}
	gotInstance, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("reload instance: %v", err)
	}
	if gotInstance.State != string(state.StateParked) || gotInstance.ParkedAt.IsZero() {
		t.Fatalf("instance after recreate = %+v, want parked with timestamp", gotInstance)
	}
	gotNode, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if gotNode.Lifecycle != state.NodeLifecycleUnavailable {
		t.Fatalf("unavailable node lifecycle = %q, want unchanged", gotNode.Lifecycle)
	}
}
