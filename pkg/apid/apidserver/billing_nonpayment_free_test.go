package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Billing review 2026-11-13 (R9-1, ADR-20261114093000): an account dunning
// moved to Free for non-payment stays there while the old subscription is
// unpaid, whatever that subscription's webhooks say, and its payment clears
// the unpaid-balance marker.
func TestMovedToFreeAccountIgnoresTheUnpaidSubscription(t *testing.T) {
	ctx := context.Background()
	srv, store, _, acct := newSubscriptionEndFixture(t, api.PlanPro, state.AccountActive, "sub_1")
	if err := store.MarkDunningStep(ctx, acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	reload := func() state.Account {
		t.Helper()
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, ev := range []billing.Event{
		{Type: billing.EventPaymentFailed, CustomerID: "polar-cust-1", SubscriptionID: "sub_1"},
		{Type: billing.EventSubscriptionPastDue, CustomerID: "polar-cust-1", SubscriptionID: "sub_1"},
		{Type: billing.EventSubscriptionUpdated, CustomerID: "polar-cust-1", SubscriptionID: "sub_1", PlanID: "pro"},
	} {
		if err := srv.HandleBillingEventWithOptions(ctx, ev, reload(), false); err != nil {
			t.Fatalf("%v: %v", ev.Type, err)
		}
		if got := reload(); !got.MovedToFreeForNonpayment() || got.Plan != api.PlanFree {
			t.Fatalf("after %v: status %s plan %s past_due_at %v; want active on Free with the balance marked", ev.Type, got.Status, got.Plan, got.PastDueAt)
		}
	}
	ev := billing.Event{Type: billing.EventPaymentSucceeded, EventID: "evt_paid", CustomerID: "polar-cust-1", SubscriptionID: "sub_1", PlanID: "pro"}
	if err := srv.HandleBillingEventWithOptions(ctx, ev, reload(), false); err != nil {
		t.Fatalf("payment succeeded: %v", err)
	}
	if got := reload(); got.Status != state.AccountActive || got.PastDueAt != nil {
		t.Fatalf("after payment: status %s past_due_at %v; want active with the balance cleared", got.Status, got.PastDueAt)
	}
}
