package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreManagedRealtimeChannelRouteSnapshot(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "realtime-route-"+suffix, "realtime-route-"+suffix)
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	nodeID := resolveDefaultLocal(t, ctx, s)
	if err := s.AddManagedRealtimeChannelRoutes(ctx, []state.ManagedRealtimeChannelRoute{{
		EndpointID: endpoint.ID,
		Channel:    "updates",
		NodeID:     nodeID,
	}}); err != nil {
		t.Fatalf("AddManagedRealtimeChannelRoutes: %v", err)
	}

	view, err := s.ListManagedRealtimeChannelRouteView(ctx, endpoint.ID, "updates")
	if err != nil || view.Disabled || len(view.NodeIDs) != 1 || view.NodeIDs[0] != nodeID {
		t.Fatalf("route snapshot = (%+v, %v), want one enabled route for %s", view, err, nodeID)
	}

	// Overflow publication and route clearing commit together. The reader must
	// observe the disabled marker with the empty index as one coherent snapshot.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin overflow transition: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		insert into managed_realtime_channel_route_overflow (endpoint_id)
		values ($1)`, endpoint.ID); err != nil {
		t.Fatalf("insert overflow marker: %v", err)
	}
	if _, err := tx.Exec(ctx, `delete from managed_realtime_channel_routes where endpoint_id = $1`, endpoint.ID); err != nil {
		t.Fatalf("clear overflow routes: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit overflow transition: %v", err)
	}
	view, err = s.ListManagedRealtimeChannelRouteView(ctx, endpoint.ID, "updates")
	if err != nil || !view.Disabled || len(view.NodeIDs) != 0 {
		t.Fatalf("overflow snapshot = (%+v, %v), want disabled and empty", view, err)
	}
}

func TestPgStoreManagedRealtimeChannelRouteSnapshotRevisionRoundTrips(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	generation, err := s.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		t.Fatalf("CurrentManagedRealtimeChannelRouteGeneration: %v", err)
	}
	want := state.ManagedRealtimeChannelRouteSnapshotRevision{InstanceID: "realtimed-instance", Revision: 42}
	if err := s.ReplaceManagedRealtimeChannelRoutesWithRevision(ctx, nodeID, generation, nil, &want); err != nil {
		t.Fatalf("ReplaceManagedRealtimeChannelRoutesWithRevision: %v", err)
	}

	revisions, err := s.ListManagedRealtimeChannelRouteSnapshotRevisions(ctx, []string{nodeID, uuid.NewString()})
	if err != nil {
		t.Fatalf("ListManagedRealtimeChannelRouteSnapshotRevisions: %v", err)
	}
	if len(revisions) != 1 || revisions[nodeID] != want {
		t.Fatalf("snapshot revisions = %+v, want %s: %+v", revisions, nodeID, want)
	}

	updated := state.ManagedRealtimeChannelRouteSnapshotRevision{InstanceID: "realtimed-instance", Revision: 43}
	if err := s.ReplaceManagedRealtimeChannelRoutesWithRevision(ctx, nodeID, generation, nil, &updated); err != nil {
		t.Fatalf("update route snapshot revision: %v", err)
	}
	revisions, err = s.ListManagedRealtimeChannelRouteSnapshotRevisions(ctx, []string{nodeID})
	if err != nil || len(revisions) != 1 || revisions[nodeID] != updated {
		t.Fatalf("updated snapshot revisions = (%+v, %v), want %s: %+v", revisions, err, nodeID, updated)
	}

	if err := s.ReplaceManagedRealtimeChannelRoutes(ctx, nodeID, generation, nil); err != nil {
		t.Fatalf("replace route snapshot without revision: %v", err)
	}
	revisions, err = s.ListManagedRealtimeChannelRouteSnapshotRevisions(ctx, []string{nodeID})
	if err != nil || len(revisions) != 0 {
		t.Fatalf("snapshot revisions after legacy replacement = (%+v, %v), want empty", revisions, err)
	}
}

func TestPgStoreManagedRealtimeChannelRouteLockReleaseAfterCancellation(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	nodeID := uuid.NewString()
	lock, err := s.AcquireManagedRealtimeChannelRouteLock(ctx, nodeID)
	if err != nil {
		t.Fatalf("AcquireManagedRealtimeChannelRouteLock: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	lock.Release(canceledCtx)

	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), time.Second)
	defer acquireCancel()
	nextLock, err := s.AcquireManagedRealtimeChannelRouteLock(acquireCtx, nodeID)
	if err != nil {
		t.Fatalf("reacquire route lock after canceled release: %v", err)
	}
	nextLock.Release(context.Background())
}
