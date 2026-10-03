// adr: 244
package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemAPIDiscoveryWithoutAuditIsReplaySafeAndScoped(t *testing.T) {
	store := NewMemStore()
	now := time.Date(2026, 9, 25, 12, 34, 12, 0, time.UTC)
	event := APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
		ConsumerKey: AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute),
		RequestCount: 1, BillableUnits: 1,
	}
	if applied, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil || !applied {
		t.Fatalf("legacy usage applied=%t err=%v", applied, err)
	}
	event.DiscoveredRoute, event.DiscoveredAt = "GET /profiles/{id}", now
	for i := 0; i < 2; i++ {
		if applied, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil || applied {
			t.Fatalf("discovery replay %d applied=%t err=%v", i, applied, err)
		}
	}
	second := event
	second.EventID, second.DiscoveredAt = uuid.NewString(), now.Add(time.Minute)
	if applied, err := store.RecordAPIConsumerUsage(context.Background(), second); err != nil || !applied {
		t.Fatalf("second request applied=%t err=%v", applied, err)
	}
	routes, capHit, err := store.ListDiscoveredAPIRoutes(context.Background(), event.AccountID, event.AppID, 500)
	if err != nil || capHit || len(routes) != 1 || routes[0].RouteTemplate != event.DiscoveredRoute || routes[0].RequestCount != 2 || !routes[0].FirstSeen.Equal(now) || !routes[0].LastSeen.Equal(second.DiscoveredAt) {
		t.Fatalf("routes=%+v capHit=%t err=%v", routes, capHit, err)
	}
	other, _, err := store.ListDiscoveredAPIRoutes(context.Background(), uuid.NewString(), event.AppID, 500)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-account routes=%+v err=%v", other, err)
	}
	usage, err := store.ListAPIConsumerUsage(context.Background(), event.AccountID, event.AppID, AnonymousConsumerKey, event.WindowStart, event.WindowStart.Add(2*time.Minute))
	if err != nil || len(usage) != 1 || usage[0].RequestCount != 2 {
		// Both requests are in one minute; no replay can add a third.
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
}

func TestMemAPIDiscoveryCapsCandidatesButUpdatesExistingRoutes(t *testing.T) {
	store := NewMemStore()
	accountID, appID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Minute)
	base := APIConsumerUsageEvent{AccountID: accountID, AppID: appID, ConsumerKey: AnonymousConsumerKey,
		WindowStart: now, RequestCount: 1, BillableUnits: 1}
	for i := 0; i <= DiscoveredRouteLimit; i++ {
		event := base
		event.EventID = uuid.NewString()
		event.DiscoveredRoute = fmt.Sprintf("GET /route-%03d", i)
		if _, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	event := base
	event.EventID, event.DiscoveredRoute = uuid.NewString(), "GET /route-000"
	if _, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	routes, capHit, err := store.ListDiscoveredAPIRoutes(context.Background(), accountID, appID, DiscoveredRouteLimit)
	if err != nil || !capHit || len(routes) != DiscoveredRouteLimit || routes[0].RequestCount != 2 || routes[len(routes)-1].RouteTemplate != "GET /route-499" {
		t.Fatalf("routes=%+v capHit=%t err=%v", routes, capHit, err)
	}
}
