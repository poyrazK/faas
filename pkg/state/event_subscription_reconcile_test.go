package state_test

// adr: 20261031130000

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An event accepted while its subscription was enabled is not delivered once
// the subscription is disabled: routing settles it as filtered and creates no
// invocation.
func TestEventRoutingAdmissionSkipsDisabledSubscription(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, _ *pgxpool.Pool, adopted bool) {
		claim, receipt, _ := seedRoutingAdmission(t, store, adopted, "")
		ctx := context.Background()
		recipient := receipt.RecipientSnapshot[0]
		if err := store.SetEventSubscriptionEnabled(ctx, recipient.ID, recipient.AccountID, recipient.AppID, false); err != nil {
			t.Fatal(err)
		}
		result, err := store.AdmitPublishedEventRecipient(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		if result.InvocationCreated || result.Progress.State != state.PublishedEventRecipientFiltered {
			t.Fatalf("disabled subscription admission = %+v, want filtered without a delivery", result)
		}
	})
}

// Re-declaring a disabled subscription enables the same row, and the quota
// counts follow the enabled subscriptions of the app and the account.
func TestEventSubscriptionReenableAndQuotaCounts(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "reconcile-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accountID := uuid.MustParse(account.ID).String()
		newApp := func() state.App {
			app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "reconcile-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			return app
		}
		first, second := newApp(), newApp()
		a, _, err := store.UpsertEventSubscription(ctx, accountID, first.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.UpsertEventSubscription(ctx, accountID, first.ID, "orders", "order.paid", nil); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.UpsertEventSubscription(ctx, accountID, second.ID, "orders", "order.created", nil); err != nil {
			t.Fatal(err)
		}
		counts := func(want [2]int) {
			t.Helper()
			app, acct, err := store.EventSubscriptionQuotaCounts(ctx, first.ID)
			if err != nil || app != want[0] || acct != want[1] {
				t.Fatalf("quota counts = %d/%d, %v; want %d/%d", app, acct, err, want[0], want[1])
			}
		}
		counts([2]int{2, 3})
		if err := store.SetEventSubscriptionEnabled(ctx, a.ID, accountID, first.ID, false); err != nil {
			t.Fatal(err)
		}
		counts([2]int{1, 2})
		again, inserted, err := store.UpsertEventSubscription(ctx, accountID, first.ID, "orders", "order.created", nil)
		if err != nil || inserted || again.ID != a.ID || !again.Enabled {
			t.Fatalf("re-declared subscription = %+v inserted=%t, %v; want the same row enabled", again, inserted, err)
		}
		counts([2]int{2, 3})
		if err := store.SetEventSubscriptionEnabled(ctx, uuid.NewString(), accountID, first.ID, false); err == nil {
			t.Fatal("disabling an unknown subscription succeeded")
		}
	})
}

// A filter on an integer above 2^53 keeps every digit instead of being
// rounded through float64.
func TestEventSubscriptionFilterKeepsLargeIntegers(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "bigint-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accountID := uuid.MustParse(account.ID).String()
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "bigint-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		filter := json.RawMessage(`{"data":{"order_id":{"$eq":9007199254740993}}}`)
		sub, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", filter)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(sub.Filter), "9007199254740993") {
			t.Fatalf("stored filter = %s, want the exact integer", sub.Filter)
		}
	})
}
