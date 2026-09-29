package state

import (
	"context"
	"testing"
)

func TestMemStoreListsActiveNodesNeedingRouteSnapshots(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()

	activeNodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	if len(activeNodes) == 0 {
		t.Fatal("ActiveComputeNodes returned no seeded node")
	}

	missing, err := store.ListManagedRealtimeChannelRouteNodesNeedingSnapshot(ctx)
	if err != nil {
		t.Fatalf("ListManagedRealtimeChannelRouteNodesNeedingSnapshot: %v", err)
	}
	if len(missing) != len(activeNodes) {
		t.Fatalf("nodes needing snapshots = %v, want all active nodes %v", missing, activeNodes)
	}
	for _, node := range activeNodes {
		current, err := store.ManagedRealtimeChannelRouteNodeSnapshotCurrent(ctx, node.ID)
		if err != nil {
			t.Fatalf("ManagedRealtimeChannelRouteNodeSnapshotCurrent(%s): %v", node.ID, err)
		}
		if current {
			t.Fatalf("node %s reported a current snapshot before bootstrap", node.ID)
		}
	}

	generation, err := store.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		t.Fatalf("CurrentManagedRealtimeChannelRouteGeneration: %v", err)
	}
	bootstrapped := activeNodes[0]
	if err := store.ReplaceManagedRealtimeChannelRoutes(ctx, bootstrapped.ID, generation, nil); err != nil {
		t.Fatalf("record empty snapshot for %s: %v", bootstrapped.ID, err)
	}
	current, err := store.ManagedRealtimeChannelRouteNodeSnapshotCurrent(ctx, bootstrapped.ID)
	if err != nil || !current {
		t.Fatalf("empty snapshot current = (%v, %v), want (true, nil)", current, err)
	}
	missing, err = store.ListManagedRealtimeChannelRouteNodesNeedingSnapshot(ctx)
	if err != nil {
		t.Fatalf("ListManagedRealtimeChannelRouteNodesNeedingSnapshot after bootstrap: %v", err)
	}
	if len(missing) != len(activeNodes)-1 {
		t.Fatalf("nodes still needing snapshots = %v, want all except %s", missing, bootstrapped.ID)
	}
	for _, nodeID := range missing {
		if nodeID == bootstrapped.ID {
			t.Fatalf("bootstrapped node %s still needs a snapshot", nodeID)
		}
	}
}
