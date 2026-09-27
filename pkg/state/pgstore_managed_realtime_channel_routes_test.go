package state_test

import (
	"testing"

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

	nodeIDs, disabled, err := s.ListManagedRealtimeChannelRouteNodeIDs(ctx, endpoint.ID, "updates")
	if err != nil || disabled || len(nodeIDs) != 1 || nodeIDs[0] != nodeID {
		t.Fatalf("route snapshot = (%v, %v, %v), want ([%s], false, nil)", nodeIDs, disabled, err, nodeID)
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
	nodeIDs, disabled, err = s.ListManagedRealtimeChannelRouteNodeIDs(ctx, endpoint.ID, "updates")
	if err != nil || !disabled || len(nodeIDs) != 0 {
		t.Fatalf("overflow snapshot = (%v, %v, %v), want ([], true, nil)", nodeIDs, disabled, err)
	}
}
