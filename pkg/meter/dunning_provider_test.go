package meter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeSubscriptionLister struct {
	subs []billing.Subscription
	err  error
}

func (f *fakeSubscriptionLister) CustomerSubscriptions(context.Context, string) ([]billing.Subscription, error) {
	return f.subs, f.err
}

// The dunning sweep asks the provider before moving an account to Free for
// non-payment: a subscription the provider reports paid up means a missed
// webhook, so the move waits for apid's reconciliation; a provider error
// waits too and fails the tick. Only a subscription still unpaid moves the
// account.
func TestDunningAsksTheProviderBeforeMovingToFree(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lister    *fakeSubscriptionLister
		wantFree  bool
		wantError bool
	}{
		{name: "paid at the provider", lister: &fakeSubscriptionLister{subs: []billing.Subscription{{ID: "sub_1", SubscriptionState: billing.SubscriptionState{Status: billing.SubscriptionStatusActive}}}}},
		{name: "still past due", lister: &fakeSubscriptionLister{subs: []billing.Subscription{{ID: "sub_1", SubscriptionState: billing.SubscriptionState{Status: billing.SubscriptionStatusPastDue}}}}, wantFree: true},
		{name: "another paid subscription is not the account's", lister: &fakeSubscriptionLister{subs: []billing.Subscription{
			{ID: "sub_1", SubscriptionState: billing.SubscriptionState{Status: billing.SubscriptionStatusPastDue}},
			{ID: "sub_2", SubscriptionState: billing.SubscriptionState{Status: billing.SubscriptionStatusActive}}}}, wantFree: true},
		{name: "provider unavailable", lister: &fakeSubscriptionLister{err: errors.New("polar 503")}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			_, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
			acct := seedPastDue(t, ctx, store, "provider-check@example.com", api.PlanPro, now.AddDate(0, 0, -8))
			if err := store.UpdateAccountProviderCustomerID(ctx, acct.ID, "cus_1"); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, "sub_1"); err != nil {
				t.Fatal(err)
			}
			d := meter.NewDunning(meter.DunningParams{Store: store, Parker: parker, Notif: notif, Mailer: mailer,
				Now: func() time.Time { return *now }, Log: discardLog(), Subscriptions: tc.lister})
			err := d.RunOnce(ctx)
			if (err != nil) != tc.wantError {
				t.Fatalf("RunOnce error = %v, want error %v", err, tc.wantError)
			}
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			moved := got.Status == state.AccountActive && got.Plan == api.PlanFree
			if moved != tc.wantFree {
				t.Fatalf("status %s plan %s; moved to Free = %v, want %v", got.Status, got.Plan, moved, tc.wantFree)
			}
		})
	}
}
