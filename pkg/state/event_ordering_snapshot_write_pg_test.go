package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// adr: 795 — accepting an event writes its outbox row back only to stamp a
// routing order on an ordered recipient. An event whose recipients are all
// unordered keeps the snapshot it was inserted with, and is not updated.
func TestPgEventOrderingSnapshotWritesOnlyOrderedEvents(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "ordering-snapshot-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "ordering-snapshot",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	// A lane is ordered when any binding of its policy is, so the unordered
	// binding has a policy of its own.
	for _, binding := range []struct {
		eventType, policy string
		ordered           bool
	}{{"order.changed", "event-order", true}, {"order.viewed", "event-plain", false}} {
		policy := workpolicy.Policy{Name: binding.policy, MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
		if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
			t.Fatal(err)
		}
		subscription, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", binding.eventType, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEventWorkBinding(ctx, app.ID, subscription.ID, policy.Name, "data.order_id",
			state.EventWorkBindingOptions{Ordered: binding.ordered}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}

	// accept inserts one event in its own transaction and reports how often
	// that transaction updated outbox rows, and the snapshot it left.
	accept := func(id, eventType string) (updates int64, snapshot []map[string]any) {
		t.Helper()
		envelope, err := (events.Envelope{ID: id, Source: "orders", Type: eventType,
			Data: json.RawMessage(`{"order_id":"order-a"}`)}).Normalize(account.ID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `INSERT INTO events (actor, kind, subject, data) VALUES ('apid', 'event.published', $1, $2)`,
			uuid.MustParse(account.ID), payload); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT
			(SELECT n_tup_upd FROM pg_stat_xact_user_tables WHERE relname = 'event_fanout_outbox'),
			(SELECT recipient_snapshot FROM event_fanout_outbox WHERE account_id = $1 AND event_id = $2)`,
			uuid.MustParse(account.ID), id).Scan(&updates, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return updates, snapshot
	}
	routingOrders := func(snapshot []map[string]any) int {
		n := 0
		for _, recipient := range snapshot {
			if work, ok := recipient["work"].(map[string]any); ok && work["routing_order"] != nil {
				n++
			}
		}
		return n
	}

	for _, tc := range []struct {
		eventType        string
		updates, ordered int
	}{
		{eventType: "order.changed", updates: 1, ordered: 1},
		{eventType: "order.viewed", updates: 0, ordered: 0},
		{eventType: "order.created", updates: 0, ordered: 0},
	} {
		updates, snapshot := accept(uuid.NewString(), tc.eventType)
		if len(snapshot) != 1 {
			t.Fatalf("%s: snapshot has %d recipients, want 1: %+v", tc.eventType, len(snapshot), snapshot)
		}
		if int(updates) != tc.updates || routingOrders(snapshot) != tc.ordered {
			t.Errorf("%s: %d outbox updates and %d routing orders, want %d and %d: %+v",
				tc.eventType, updates, routingOrders(snapshot), tc.updates, tc.ordered, snapshot)
		}
	}
}
