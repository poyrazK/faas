// Package billingsync reconciles billed accounts with their provider's
// subscriptions (ADR-20261029094512). Webhook deliveries stay the primary
// path; a pass repairs what a missed, rejected or misordered delivery left
// behind, so a provider that disabled apid's webhook endpoint, or an apid
// outage longer than the provider's retries, cannot leave an account's plan
// or dunning state wrong for good.
package billingsync

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Decide compares an account with its customer's provider subscriptions and
// returns the billing event that brings the account in line, or false when
// they agree. The event is the one a webhook delivery would have carried, so
// a correction takes exactly the transition (and mail) the delivery would
// have: a paid-up subscription ends dunning, a past-due one starts it, an
// ended one downgrades to Free, and a changed product or subscription is
// re-bound. planOf maps a provider plan ID to a plan.
//
// The account's own subscription, when still live, is preferred over a newer
// one. An account is downgraded only when the provider lists its subscription
// as ended: a subscription missing from the listing proves nothing.
func Decide(acct state.Account, subs []billing.Subscription, planOf func(string) api.Plan) (billing.Event, bool) {
	bound := acct.StripeSubscriptionItem
	var live *billing.Subscription
	boundEnded := false
	for i := range subs {
		sub := &subs[i]
		switch {
		case sub.Status.Live() && (live == nil || sub.ID == bound):
			live = sub
		case sub.ID == bound && bound != "":
			boundEnded = true
		}
	}
	ev := billing.Event{CustomerID: acct.ProviderCustomerID}
	if live == nil {
		if !boundEnded {
			return billing.Event{}, false
		}
		ev.Type, ev.SubscriptionID = billing.EventSubscriptionCanceled, bound
		return ev, true
	}
	ev.SubscriptionID, ev.PlanID = live.ID, live.PlanID
	switch {
	case live.Status.InGoodStanding() && inDunning(acct):
		ev.Type = billing.EventPaymentSucceeded
		return ev, true
	case !live.Status.InGoodStanding() && acct.Status == state.AccountActive:
		ev.Type = billing.EventPaymentFailed
		return ev, true
	}
	if plan := planOf(live.PlanID); live.ID != bound || plan != "" && plan != acct.Plan {
		ev.Type = billing.EventSubscriptionUpdated
		return ev, true
	}
	return billing.Event{}, false
}

// inDunning reports whether a payment success would restore the account: the
// statuses restoreBillingPayment moves back to active.
func inDunning(acct state.Account) bool {
	switch acct.Status {
	case state.AccountPastDue:
		return true
	case state.AccountSuspended, state.AccountDeletedPending:
		return acct.PastDueAt != nil
	}
	return false
}
