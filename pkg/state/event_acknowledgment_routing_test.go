package state_test

// adr: 1411

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// A subscription that opted into acknowledgments gets the lease on every
// delivery the routing path creates, so the scheduler offers the consumer a
// token. Before this fix only the legacy path stamped it, and an
// opted-in consumer's 202 ended the delivery at once.
func TestEventRoutingAdmissionStampsAcknowledgmentLease(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		ctx := context.Background()
		acks, ok := any(store).(state.EventAcknowledgmentStore)
		if !ok {
			t.Fatal("store has no event acknowledgments")
		}
		account, err := store.CreateAccount(ctx, "ack-lease-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accountID := uuid.MustParse(account.ID).String()
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "ack-lease-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		sub, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		lease := 60
		if _, err := acks.EventAcknowledgmentPolicy(ctx, accountID, app.ID, sub.ID, &lease); err != nil {
			t.Fatal(err)
		}
		envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(accountID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
			t.Fatal(err)
		}
		receipt, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		claim := state.PublishedEventRoutingClaim{OutboxID: receipt.ID, SubscriptionID: sub.ID, ClaimToken: receipt.ClaimToken}
		if adopted {
			if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			claim.ClaimToken, claim.Generation = work.ClaimToken, work.Generation
		}
		result, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil || !result.InvocationCreated {
			t.Fatalf("admission = %+v, %v; want a delivery", result, err)
		}
		inv, err := store.InvocationByID(ctx, result.DeliveryID)
		if err != nil {
			t.Fatal(err)
		}
		if seconds, err := state.EventAcknowledgmentSeconds(inv); err != nil || seconds != lease {
			t.Fatalf("delivery acknowledgment lease = %d, %v; want %d (headers %s)", seconds, err, lease, inv.Headers)
		}
	})
}
