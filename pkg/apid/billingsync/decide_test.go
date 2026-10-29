package billingsync_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/billingsync"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func sub(id string, status billing.SubscriptionStatus, plan string) billing.Subscription {
	return billing.Subscription{ID: id, SubscriptionState: billing.SubscriptionState{Status: status, PlanID: plan}}
}

func TestDecide(t *testing.T) {
	pastDueAt := time.Now()
	const (
		active   = billing.SubscriptionStatusActive
		trialing = billing.SubscriptionStatusTrialing
		pastDue  = billing.SubscriptionStatusPastDue
		canceled = billing.SubscriptionStatus("canceled")
	)
	pro := state.Account{ID: "a", Plan: api.PlanPro, Status: state.AccountActive, ProviderCustomerID: "cus", StripeSubscriptionItem: "sub-1"}
	with := func(f func(*state.Account)) state.Account {
		acct := pro
		f(&acct)
		return acct
	}
	for _, tc := range []struct {
		name     string
		acct     state.Account
		subs     []billing.Subscription
		want     billing.EventType
		wantSub  string
		wantPlan string
	}{
		{name: "in line", acct: pro, subs: []billing.Subscription{sub("sub-1", active, "pro")}},
		{name: "trialing in line", acct: pro, subs: []billing.Subscription{sub("sub-1", trialing, "pro")}},
		{name: "no subscriptions, none bound", acct: with(func(a *state.Account) { a.Plan, a.StripeSubscriptionItem = api.PlanFree, "" })},
		{name: "comped plan without a subscription", acct: with(func(a *state.Account) { a.StripeSubscriptionItem = "" }),
			subs: []billing.Subscription{sub("old", canceled, "hobby")}},
		{name: "missed revoke", acct: pro, subs: []billing.Subscription{sub("sub-1", canceled, "pro")},
			want: billing.EventSubscriptionCanceled, wantSub: "sub-1"},
		{name: "stale binding on a free account", acct: with(func(a *state.Account) { a.Plan = api.PlanFree }),
			subs: []billing.Subscription{sub("sub-1", canceled, "pro")}, want: billing.EventSubscriptionCanceled, wantSub: "sub-1"},
		// A listing without the account's subscription proves nothing.
		{name: "bound subscription not listed", acct: pro, subs: []billing.Subscription{sub("other", canceled, "pro")}},
		{name: "missed activation", acct: with(func(a *state.Account) { a.Plan, a.StripeSubscriptionItem = api.PlanFree, "" }),
			subs: []billing.Subscription{sub("sub-2", active, "hobby")}, want: billing.EventSubscriptionUpdated, wantSub: "sub-2", wantPlan: "hobby"},
		{name: "missed plan change", acct: pro, subs: []billing.Subscription{sub("sub-1", active, "scale")},
			want: billing.EventSubscriptionUpdated, wantSub: "sub-1", wantPlan: "scale"},
		{name: "resubscribed after the bound one ended", acct: pro,
			subs: []billing.Subscription{sub("sub-2", active, "pro"), sub("sub-1", canceled, "pro")},
			want: billing.EventSubscriptionUpdated, wantSub: "sub-2", wantPlan: "pro"},
		{name: "bound subscription preferred over a newer live one", acct: pro,
			subs: []billing.Subscription{sub("sub-2", active, "hobby"), sub("sub-1", active, "pro")}},
		{name: "unmapped product keeps the plan", acct: pro, subs: []billing.Subscription{sub("sub-1", active, "")}},
		{name: "missed payment recovery", acct: with(func(a *state.Account) { a.Status, a.PastDueAt = state.AccountPastDue, &pastDueAt }),
			subs: []billing.Subscription{sub("sub-1", active, "pro")}, want: billing.EventPaymentSucceeded, wantSub: "sub-1", wantPlan: "pro"},
		{name: "recovery after dunning suspension", acct: with(func(a *state.Account) { a.Status, a.PastDueAt = state.AccountSuspended, &pastDueAt }),
			subs: []billing.Subscription{sub("sub-1", active, "pro")}, want: billing.EventPaymentSucceeded, wantSub: "sub-1", wantPlan: "pro"},
		// An operator suspension is not dunning: a paid-up subscription does
		// not lift it.
		{name: "operator suspension", acct: with(func(a *state.Account) { a.Status = state.AccountSuspended }),
			subs: []billing.Subscription{sub("sub-1", active, "pro")}},
		{name: "missed payment failure", acct: pro, subs: []billing.Subscription{sub("sub-1", pastDue, "pro")},
			want: billing.EventPaymentFailed, wantSub: "sub-1", wantPlan: "pro"},
		{name: "past due in line", acct: with(func(a *state.Account) { a.Status, a.PastDueAt = state.AccountPastDue, &pastDueAt }),
			subs: []billing.Subscription{sub("sub-1", pastDue, "pro")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, drift := billingsync.Decide(tc.acct, tc.subs, func(id string) api.Plan { return api.Plan(id) })
			if tc.want == billing.EventUnknown {
				if drift {
					t.Fatalf("Decide = %s %+v, want no correction", ev.Type.Name(), ev)
				}
				return
			}
			if !drift || ev.Type != tc.want || ev.SubscriptionID != tc.wantSub || ev.PlanID != tc.wantPlan || ev.CustomerID != tc.acct.ProviderCustomerID {
				t.Fatalf("Decide = %v %s %+v, want %s sub=%s plan=%s", drift, ev.Type.Name(), ev, tc.want.Name(), tc.wantSub, tc.wantPlan)
			}
		})
	}
}
