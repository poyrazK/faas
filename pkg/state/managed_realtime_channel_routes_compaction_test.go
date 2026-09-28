package state

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMemStoreManagedRealtimeChannelRouteRebuildLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	const endpointID = "endpoint"
	const nodeID = "node-1"

	routes := make([]ManagedRealtimeChannelRoute, 0, managedRealtimeChannelRouteLimit+1)
	for i := 0; i <= managedRealtimeChannelRouteLimit; i++ {
		routes = append(routes, ManagedRealtimeChannelRoute{
			EndpointID: endpointID,
			Channel:    fmt.Sprintf("updates-%d", i),
			NodeID:     nodeID,
		})
	}
	if err := store.AddManagedRealtimeChannelRoutes(ctx, routes); err != nil {
		t.Fatalf("AddManagedRealtimeChannelRoutes over limit: %v", err)
	}
	view, err := store.ListManagedRealtimeChannelRouteView(ctx, endpointID, "updates-0")
	if err != nil || !view.Disabled || len(view.NodeIDs) != 0 {
		t.Fatalf("overflow route view = (%+v, %v), want disabled and empty", view, err)
	}

	store.mu.Lock()
	overflow := store.realtimeChannelRouteOverflow[endpointID]
	overflow.NextRebuildAt = time.Now().Add(-time.Second)
	store.realtimeChannelRouteOverflow[endpointID] = overflow
	store.mu.Unlock()

	started, err := store.BeginManagedRealtimeChannelRouteRebuild(ctx)
	if err != nil || !started {
		t.Fatalf("BeginManagedRealtimeChannelRouteRebuild = (%v, %v), want started", started, err)
	}
	if started, err = store.BeginManagedRealtimeChannelRouteRebuild(ctx); err != nil || started {
		t.Fatalf("duplicate rebuild = (%v, %v), want not started", started, err)
	}
	generation, err := store.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		t.Fatalf("CurrentManagedRealtimeChannelRouteGeneration: %v", err)
	}
	if err := store.ReplaceManagedRealtimeChannelRoutes(ctx, nodeID, generation, []ManagedRealtimeChannelRoute{{
		EndpointID: endpointID,
		Channel:    "current",
		NodeID:     nodeID,
	}}); err != nil {
		t.Fatalf("ReplaceManagedRealtimeChannelRoutes: %v", err)
	}
	view, err = store.ListManagedRealtimeChannelRouteView(ctx, endpointID, "current")
	if err != nil || !view.Disabled || len(view.NodeIDs) != 1 || view.NodeIDs[0] != nodeID || len(view.ReadyNodeIDs) != 1 {
		t.Fatalf("rebuilding route view = (%+v, %v), want disabled route and ready node", view, err)
	}

	finalized, err := store.FinalizeManagedRealtimeChannelRouteRebuild(ctx)
	if err != nil || !finalized {
		t.Fatalf("FinalizeManagedRealtimeChannelRouteRebuild = (%v, %v), want finalized", finalized, err)
	}
	view, err = store.ListManagedRealtimeChannelRouteView(ctx, endpointID, "current")
	if err != nil || view.Disabled || len(view.NodeIDs) != 1 || view.NodeIDs[0] != nodeID {
		t.Fatalf("final route view = (%+v, %v), want enabled route for %s", view, err, nodeID)
	}
	if err := store.ReplaceManagedRealtimeChannelRoutes(ctx, nodeID, generation, []ManagedRealtimeChannelRoute{{
		EndpointID: endpointID,
		Channel:    "wrong-node",
		NodeID:     "node-2",
	}}); err == nil {
		t.Fatal("ReplaceManagedRealtimeChannelRoutes accepted a route for another node")
	}
}

func TestMemStoreManagedRealtimeChannelRouteLockReleaseAfterCancellation(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	lock, err := store.AcquireManagedRealtimeChannelRouteLock(ctx, "node-1")
	if err != nil {
		t.Fatalf("AcquireManagedRealtimeChannelRouteLock: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	lock.Release(canceledCtx)

	nextLock, err := store.AcquireManagedRealtimeChannelRouteLock(ctx, "node-1")
	if err != nil {
		t.Fatalf("reacquire route lock after canceled release: %v", err)
	}
	nextLock.Release(ctx)

	heldLock, err := store.AcquireManagedRealtimeChannelRouteLock(ctx, "node-1")
	if err != nil {
		t.Fatalf("AcquireManagedRealtimeChannelRouteLock for cancel case: %v", err)
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	cancelWait()
	if _, err := store.AcquireManagedRealtimeChannelRouteLock(waitCtx, "node-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lock acquisition error = %v, want context.Canceled", err)
	}
	heldLock.Release(ctx)
}
