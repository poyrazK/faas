package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRequestAuditReplayAndAccountScopedRead(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	now := time.Now().UTC()
	event := state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey,
		WindowStart: now.Truncate(time.Minute), RequestCount: 1, BillableUnits: 1,
	}
	if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || !applied {
		t.Fatalf("usage write applied=%v err=%v", applied, err)
	}
	event.Audit = &state.RequestAuditEvidence{
		RouteTemplate: "GET /orders/{id}", Method: "GET", HTTPStatus: 200,
		LatencyMS: 18, OccurredAt: now, SourceIP: "203.0.113.42",
	}
	for i := 0; i < 2; i++ {
		if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || applied {
			t.Fatalf("audit replay %d applied=%v err=%v", i, applied, err)
		}
	}
	rows, err := store.ListRequestAudit(ctx, accountID, appID, now.Add(-time.Minute), now.Add(time.Minute), 100)
	if err != nil || len(rows) != 1 || rows[0].SourceIP != "203.0.113.42" {
		t.Fatalf("audit rows=%+v err=%v", rows, err)
	}
	routes, capHit, err := store.ListDiscoveredAPIRoutes(ctx, accountID, appID, 100)
	if err != nil || capHit || len(routes) != 1 || routes[0].RouteTemplate != "GET /orders/{id}" || routes[0].RequestCount != 1 {
		t.Fatalf("routes=%+v capHit=%t err=%v", routes, capHit, err)
	}
	other, err := store.ListRequestAudit(ctx, uuid.NewString(), appID, now.Add(-time.Minute), now.Add(time.Minute), 100)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-account rows=%+v err=%v", other, err)
	}
}
