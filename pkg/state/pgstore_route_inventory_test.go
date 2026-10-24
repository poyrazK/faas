package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// A rehearsal-enrolled app's candidate routes stay in its own inventory but
// never fan out to the account's other apps (ADR-20261019180000).
func TestPgAPIDiscoveryOfRehearsalAppDoesNotFanOut(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	for _, enrolled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enrolled", false: "ordinary"}[enrolled], func(t *testing.T) {
			accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
			if enrolled {
				if _, err := pool.Exec(ctx, `insert into scenario_test_action_policies(app_id, account_id, run_id, webhook_mode)
					values ($1::uuid, $2::uuid, $3, 'capture')`, appID, accountID, strings.ReplaceAll(uuid.NewString(), "-", "")); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			event := state.APIConsumerUsageEvent{
				EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
				ConsumerKey: state.AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute),
				RequestCount: 1, BillableUnits: 1, DiscoveredRoute: "GET /orders/{id}", DiscoveredAt: now,
			}
			if _, err := store.RecordAPIConsumerUsage(ctx, event); err != nil {
				t.Fatal(err)
			}
			routes, _, err := store.ListDiscoveredAPIRoutes(ctx, accountID, appID, 500)
			if err != nil || len(routes) != 1 {
				t.Fatalf("routes=%+v err=%v; the app's own inventory must keep the route", routes, err)
			}
			var events, fanout int
			if err := pool.QueryRow(ctx, `select (select count(*) from events where subject = $1::uuid and kind = 'event.published' and data->>'source' = 'gregale.api'),
				(select count(*) from event_fanout_outbox where account_id = $1::uuid and source = 'gregale.api')`, accountID).Scan(&events, &fanout); err != nil {
				t.Fatal(err)
			}
			want := 1
			if enrolled {
				want = 0
			}
			if events != want || fanout != want {
				t.Fatalf("route events=%d fanout=%d, want %d", events, fanout, want)
			}
		})
	}
}

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
