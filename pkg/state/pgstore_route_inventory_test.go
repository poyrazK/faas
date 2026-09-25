package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPIDiscoveryReplayAndAccountScope(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	now := time.Now().UTC()
	event := state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute),
		RequestCount: 1, BillableUnits: 1,
	}
	if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || !applied {
		t.Fatalf("legacy usage applied=%t err=%v", applied, err)
	}
	event.DiscoveredRoute, event.DiscoveredAt = "GET /orders/{id}", now
	for i := 0; i < 2; i++ {
		if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || applied {
			t.Fatalf("discovery replay %d applied=%t err=%v", i, applied, err)
		}
	}
	routes, capHit, err := store.ListDiscoveredAPIRoutes(ctx, accountID, appID, 500)
	if err != nil || capHit || len(routes) != 1 || routes[0].RouteTemplate != event.DiscoveredRoute || routes[0].RequestCount != 1 {
		t.Fatalf("routes=%+v capHit=%t err=%v", routes, capHit, err)
	}
	other, _, err := store.ListDiscoveredAPIRoutes(ctx, uuid.NewString(), appID, 500)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-account routes=%+v err=%v", other, err)
	}
}
