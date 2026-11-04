package state_test

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

// An event published again with the same source and id after its receipt was
// pruned is a new event: deduplication lasts as long as the receipt. The
// earlier delivery outlives the receipt, so the original delivery identity
// collided and routing failed after every retry. The new acceptance now
// delivers under its own identity, and the receipt's views follow it.
//
// adr: 20261026010000
func TestRepublishAfterReceiptPruneDeliversAgain(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "republish-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "republish-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		sub, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		route := func(data string) state.PublishedEventRoutingResult {
			t.Helper()
			envelope, err := (events.Envelope{ID: "order-1", Source: "orders", Type: "order.created", Data: json.RawMessage(data)}).Normalize(account.ID, time.Now().UTC())
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
			receipt, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			result, err := store.AdmitPublishedEventRecipient(ctx, state.PublishedEventRoutingClaim{OutboxID: receipt.ID, SubscriptionID: sub.ID,
				ClaimToken: work.ClaimToken, Generation: work.Generation})
			if err != nil || !result.InvocationCreated || result.Progress.State != state.PublishedEventRecipientEnqueued {
				t.Fatalf("route %s = %+v, %v", data, result, err)
			}
			return result
		}
		prune := func() {
			t.Helper()
			if n, err := store.PruneSettledPublishedEvents(ctx, time.Now().Add(30*24*time.Hour), 100); err != nil || n != 1 {
				t.Fatalf("prune settled receipts = %d, %v", n, err)
			}
		}
		receiptDelivery := func() string {
			t.Helper()
			receipt, err := store.EventReceipt(ctx, account.ID, "orders", "order-1", state.EventReceiptCursor{}, 10)
			if err != nil || len(receipt.Recipients) != 1 || receipt.Recipients[0].Execution == nil {
				t.Fatalf("receipt = %+v, %v", receipt, err)
			}
			return receipt.Recipients[0].Execution.InvocationID
		}

		original := state.PublishedEventInvocationID(account.ID, "orders", "order-1", sub.ID)
		first := route(`{"order_id":"o-1"}`)
		if first.DeliveryID != original || first.Progress.DeliveryID != "" || receiptDelivery() != original {
			t.Fatalf("first acceptance = %+v, want the original delivery %s", first, original)
		}
		prune()

		again := route(`{"order_id":"o-1"}`)
		if again.DeliveryID == original || again.Progress.DeliveryID != again.DeliveryID {
			t.Fatalf("re-acceptance = %+v, want a delivery of its own recorded on the receipt", again)
		}
		if inv, err := store.InvocationByID(ctx, again.DeliveryID); err != nil || inv.State != state.InvocationPending {
			t.Fatalf("re-accepted delivery = %+v, %v", inv, err)
		}
		if got := receiptDelivery(); got != again.DeliveryID {
			t.Fatalf("receipt shows delivery %s, want %s", got, again.DeliveryID)
		}
		if attempts, err := store.EventReceiptAttempts(ctx, account.ID, "orders", "order-1", sub.ID, state.EventReceiptAttemptCursor{}, 10); err != nil || attempts.OriginalInvocationID != again.DeliveryID {
			t.Fatalf("attempt history = %+v, %v", attempts, err)
		}
		if replays, err := store.EventReceiptReplays(ctx, account.ID, "orders", "order-1", sub.ID, state.EventReceiptReplayCursor{}, 10); err != nil || replays.OriginalInvocationID != again.DeliveryID {
			t.Fatalf("replay history = %+v, %v", replays, err)
		}

		// Changed content after the window is a new event too.
		prune()
		changed := route(`{"order_id":"o-2"}`)
		if changed.DeliveryID == original || changed.DeliveryID == again.DeliveryID {
			t.Fatalf("changed re-acceptance = %+v, want a new delivery", changed)
		}
	})
}
