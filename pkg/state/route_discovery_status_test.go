package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestAuditedClientErrorIsNoDiscoveredRoute: an audited request the app
// answered 404 (a scanner probe) became a discovered API route through the
// audit fallback (Hunt 12 H12-17); a served request still does.
func TestAuditedClientErrorIsNoDiscoveredRoute(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now().UTC().Truncate(time.Minute)
	account, app := uuid.NewString(), uuid.NewString()
	for route, status := range map[string]int{"GET /wp-login.php": 404, "GET /orders": 200} {
		event := state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: account, AppID: app,
			ConsumerKey: state.AnonymousConsumerKey, WindowStart: now,
			RequestCount: 1, BillableUnits: 1,
			Audit: &state.RequestAuditEvidence{RouteTemplate: route, Method: "GET", HTTPStatus: status, OccurredAt: now.Add(time.Second), RequestID: route},
		}
		if _, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
			t.Fatalf("%s: %v", route, err)
		}
	}
	routes, _, err := store.ListDiscoveredAPIRoutes(context.Background(), account, app, 100)
	if err != nil || len(routes) != 1 || routes[0].RouteTemplate != "GET /orders" {
		t.Fatalf("routes=%+v err=%v; want only the served route", routes, err)
	}
}
