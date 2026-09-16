package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetiredComputeNodeRejectsLegacyAvailabilityWrites(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	node, err := store.CreateComputeNode(ctx, ComputeNode{
		Name: "retired-node", TargetURL: "unix:///run/faas/vmmd-retired.sock",
		Lifecycle: NodeLifecycleRetired,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if NodeLifecycleRetired.IsAdmitting() {
		t.Fatal("retired lifecycle unexpectedly admits placement")
	}
	if err := store.SetComputeNodeActive(ctx, node.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("SetComputeNodeActive(retired)=%v, want ErrConflict", err)
	}
	if err := store.MarkComputeNodeInactive(ctx, node.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("MarkComputeNodeInactive(retired)=%v, want ErrConflict", err)
	}
	if _, err := store.UpsertComputeNodeFromOperator(ctx, ComputeNode{
		Name: node.Name, TargetURL: "unix:///run/faas/new.sock",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("operator re-enrollment=%v, want ErrConflict", err)
	}
	refreshed, err := store.UpsertComputeNodeFromVmmd(ctx, ComputeNode{
		Name: node.Name, TargetURL: "unix:///run/faas/vmmd.sock",
	})
	if err != nil || refreshed.Lifecycle != NodeLifecycleRetired {
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
	store := NewMemStore()
	node, err := store.CreateComputeNode(ctx, ComputeNode{
		Name: "long-gone-node", TargetURL: "unix:///run/faas/vmmd-gone.sock",
		Lifecycle: NodeLifecycleUnavailable,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	stale := store.computeNodes[node.ID]
	stale.LastHeartbeatAt = time.Now().Add(-25 * time.Hour)
	store.computeNodes[node.ID] = stale
	store.mu.Unlock()

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
