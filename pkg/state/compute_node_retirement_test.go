package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestRetiredComputeNodeRejectsLegacyAvailabilityWrites(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "retired-node", TargetURL: "unix:///run/faas/vmmd-retired.sock",
		Lifecycle: state.NodeLifecycleRetired,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if state.NodeLifecycleRetired.IsAdmitting() {
		t.Fatal("retired lifecycle unexpectedly admits placement")
	}
	if err := store.SetComputeNodeActive(ctx, node.ID, true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("SetComputeNodeActive(retired)=%v, want ErrConflict", err)
	}
	if err := store.MarkComputeNodeInactive(ctx, node.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MarkComputeNodeInactive(retired)=%v, want ErrConflict", err)
	}
	if _, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name: node.Name, TargetURL: "unix:///run/faas/new.sock",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("operator re-enrollment=%v, want ErrConflict", err)
	}
	refreshed, err := store.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{
		Name: node.Name, TargetURL: "unix:///run/faas/vmmd.sock",
	})
	if err != nil || refreshed.Lifecycle != state.NodeLifecycleRetired {
		t.Fatalf("vmmd refresh revived retired node: node=%+v err=%v", refreshed, err)
	}
	recoverable, err := store.NodeListRecoverable(ctx)
	if err != nil {
		t.Fatalf("NodeListRecoverable: %v", err)
	}
	for _, candidate := range recoverable {
		if candidate.ID == node.ID {
			t.Fatal("retired node appeared in recovery input")
		}
	}
}

func TestTerminallyStaleUnavailableNodeIsNotRecoveryWork(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "long-gone-node", TargetURL: "unix:///run/faas/vmmd-gone.sock",
		Lifecycle: state.NodeLifecycleUnavailable,
	})
	if err != nil {
		t.Fatal(err)
	}
	(*store.Mu()).Lock()
	stale := (*store.ComputeNodes())[node.ID]
	stale.LastHeartbeatAt = time.Now().Add(-25 * time.Hour)
	(*store.ComputeNodes())[node.ID] = stale
	(*store.Mu()).Unlock()

	recoverable, err := store.NodeListRecoverable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range recoverable {
		if candidate.ID == node.ID {
			t.Fatalf("terminally stale node remained in recovery work: %+v", candidate)
		}
	}
}
