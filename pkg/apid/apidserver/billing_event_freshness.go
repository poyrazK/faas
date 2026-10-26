package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// billingStateTimeout bounds the provider read on the webhook path, so a slow
// provider API cannot hold the delivery past the provider's own timeout.
const billingStateTimeout = 5 * time.Second

// freshBillingEvent compares a verified delivery with the provider's current
// subscription state (Hunt 17 H17-1). Deliveries are neither ordered nor
// prompt: a payment failure retried after the customer paid would restart
// dunning on a paying account, and an update for a subscription that has
// since ended would re-bind it. stale=true means acknowledge the delivery
// without a transition.
//
// The plan a delivery grants is the subscription's current product, not the
// one the delivery carried. A provider that cannot report state, a delivery
// without a subscription, or a failed read keeps the delivery as sent.
func (s *server) freshBillingEvent(ctx context.Context, ev billing.Event) (billing.Event, bool) {
	switch ev.Type {
	case billing.EventPaymentFailed, billing.EventSubscriptionPastDue,
		billing.EventSubscriptionUpdated, billing.EventPaymentSucceeded:
	default:
		return ev, false
	}
	reader, ok := s.billingProvider.(billing.SubscriptionStateProvider)
	if !ok || ev.SubscriptionID == "" {
		return ev, false
	}
	readCtx, cancel := context.WithTimeout(ctx, billingStateTimeout)
	defer cancel()
	current, err := reader.SubscriptionState(readCtx, ev.SubscriptionID)
	if err != nil {
		s.log.Warn("billing webhook: subscription state unavailable; applying the delivery as sent",
			"event_type", ev.Type.Name(), "event_id", logsanitize.Field(ev.EventID), "err", logsanitize.Field(err.Error()))
		return ev, false
	}
	switch ev.Type {
	case billing.EventPaymentFailed, billing.EventSubscriptionPastDue:
		return ev, current.Status.InGoodStanding()
	case billing.EventSubscriptionUpdated:
		if !current.Status.Live() {
			return ev, true
		}
	case billing.EventPaymentSucceeded:
		if !current.Status.Live() {
			// The payment itself stands (invoice, credits, dunning
			// recovery), but an ended subscription is never re-bound and
			// its plan is never granted again.
			ev.SubscriptionID, ev.PlanID = "", ""
			return ev, false
		}
	}
	if current.PlanID != "" {
		ev.PlanID = current.PlanID
	}
	return ev, false
}
