package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreNodeListDrainableHonorsLiveInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	idle, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "drainable-idle",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create idle node: %v", err)
	}
	busy, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "drainable-busy",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create busy node: %v", err)
	}

	for i, instanceState := range []state.State{
		state.StateWaking,
		state.StateColdBooting,
		state.StateRunning,
		state.StateSnapshotting,
		state.StateMigrating,
	} {
		if _, err := store.CreateInstance(ctx, "app-drain", "dep-drain", string(instanceState), 128, busy.ID, "wake-drain"); err != nil {
			t.Fatalf("create live instance %d (%s): %v", i, instanceState, err)
		}
	}
	if _, err := store.CreateInstance(ctx, "app-drain", "dep-parked", string(state.StateParked), 128, busy.ID, "wake-parked"); err != nil {
		t.Fatalf("create parked instance: %v", err)
	}

	nodes, err := store.NodeListDrainable(ctx)
	if err != nil {
		t.Fatalf("NodeListDrainable: %v", err)
	}
	foundIdle, foundBusy := false, false
	for _, node := range nodes {
		foundIdle = foundIdle || node.ID == idle.ID
		foundBusy = foundBusy || node.ID == busy.ID
	}
	if !foundIdle || foundBusy {
		t.Fatalf("drainable nodes = %+v, want idle node %q and no busy node %q", nodes, idle.ID, busy.ID)
	}
}

func TestMemStoreUpdateInstanceStateIfStampsParkedAt(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	instance, err := store.CreateInstance(ctx, "app-recovery", "dep-recovery", string(state.StateRunning), 128, "node-recovery", "wake-recovery")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if err := store.UpdateInstanceStateIf(ctx, instance.ID, string(state.StateRunning), string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceStateIf: %v", err)
	}
	parked, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("reload parked instance: %v", err)
	}
	if parked.State != string(state.StateParked) || parked.ParkedAt.IsZero() {
		t.Fatalf("parked instance = %+v, want parked state and timestamp", parked)
	}
	parkedAt := parked.ParkedAt

	if err := store.UpdateInstanceStateIf(ctx, instance.ID, string(state.StateRunning), string(state.StateFailed)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale UpdateInstanceStateIf error = %v, want ErrConflict", err)
	}
	unchanged, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("reload unchanged instance: %v", err)
	}
	if unchanged.State != string(state.StateParked) || !unchanged.ParkedAt.Equal(parkedAt) {
		t.Fatalf("stale update changed instance: %+v", unchanged)
	}
}

func TestMemStoreListInstancesOnNodeIDUsesPhysicalPlacement(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "physical-placement@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "physical-placement",
		NodeID:    "owner-node",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, "dep-physical", string(state.StateRunning), 128, "physical-node", "wake-physical")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if got, err := store.ListInstancesByNodeID(ctx, "physical-node"); err != nil {
		t.Fatalf("ownership query: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("ownership query returned %d instances, want 0", len(got))
	}
	got, err := store.ListInstancesOnNodeID(ctx, "physical-node")
	if err != nil {
		t.Fatalf("physical query: %v", err)
	}
	if len(got) != 1 || got[0].ID != instance.ID {
		t.Fatalf("physical query = %+v, want instance %q", got, instance.ID)
	}
}
