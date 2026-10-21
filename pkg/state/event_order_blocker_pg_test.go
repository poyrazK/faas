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

// adr: 795 — ordering lanes are work-policy lanes. A recipient without a
// work policy returns before the backlog scan and is never blocked, while an
// ordered lane is still held behind an earlier unsettled event of its key.
func TestPgEventOrderBlockerSkipsRecipientsWithoutWorkPolicy(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "order-blocker-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: slug,
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	orderedApp, plainApp := newApp("ordered-consumer"), newApp("plain-consumer")
	policy := workpolicy.Policy{Name: "event-order", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, orderedApp.ID, policy); err != nil {
		t.Fatal(err)
	}
	ordered, _, err := store.UpsertEventSubscription(ctx, account.ID, orderedApp.ID, "orders", "order.changed", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEventWorkBinding(ctx, orderedApp.ID, ordered.ID, policy.Name, "data.order_id",
		state.EventWorkBindingOptions{Ordered: true}); err != nil {
		t.Fatal(err)
	}
	plain, _, err := store.UpsertEventSubscription(ctx, account.ID, plainApp.ID, "orders", "order.changed", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.changed",
			Data: json.RawMessage(`{"order_id":"order-a"}`)}).Normalize(account.ID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
			t.Fatal(err)
		}
	}
	// Adopt the first event so its recipients are pending: the earlier,
	// unsettled event of order-a.
	now := time.Now().UTC()
	first, err := store.ClaimDuePublishedEvent(ctx, now)
	if err != nil || receiptEventID(t, first) != "first" {
		t.Fatalf("first receipt = %+v, %v", first, err)
	}
	if err := store.InitializePublishedEventRecipients(ctx, first, now); err != nil {
		t.Fatal(err)
	}

	blocked := func(subscriptionID string) bool {
		t.Helper()
		var isBlocked bool
		if err := pool.QueryRow(ctx, `SELECT event_recipient_order_blocked(o.id, $2, item, true)
			FROM event_fanout_outbox o CROSS JOIN LATERAL jsonb_array_elements(o.recipient_snapshot) item
			WHERE o.account_id = $1 AND o.event_id = 'second' AND item->>'id' = $2`,
			account.ID, subscriptionID).Scan(&isBlocked); err != nil {
			t.Fatalf("order blocked for %s: %v", subscriptionID, err)
		}
		return isBlocked
	}
	if !blocked(ordered.ID) {
		t.Fatal("ordered lane recipient was not held behind the earlier unsettled event of its key")
	}
	if blocked(plain.ID) {
		t.Fatal("recipient without a work policy was blocked by an ordering lane")
	}
}
