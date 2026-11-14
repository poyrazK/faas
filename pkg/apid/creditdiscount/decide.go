// Package creditdiscount applies account credit as a one-time fixed discount
// on the billing provider subscription's next order, instead of refunding a
// paid order (ADR-20261104212028). apid owns it (ADR-982).
//
// A discount reduces the whole order before tax: the plan fee and every
// usage line. Its amount is the subject's credit that is still active when the
// subscription renews; the provider applies at most the order's subtotal, and
// only what it applied is drawn from the credits. Credit that cannot be
// attached, because the subscription is ending, has a plan change pending or
// carries a discount Gregale did not create, stays on the account for a later
// order. It is never refunded.
package creditdiscount

import (
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Action is what a sync does to the subscription.
type Action int

const (
	// Keep changes nothing: the right discount is attached, or there is
	// neither credit nor a discount.
	Keep Action = iota
	// Attach creates a discount for Amount and puts it on the subscription,
	// replacing Gregale's discount there.
	Attach
	// Detach removes Gregale's discount: no credit is left for it.
	Detach
	// Hold leaves the subscription alone and the credit for later (Reason).
	Hold
)

// Hold reasons.
const (
	ReasonNoSubscription   = "no_subscription"
	ReasonNotAccepting     = "subscription_not_accepting_changes"
	ReasonForeignDiscount  = "other_discount_attached"
	ReasonCurrencyUnknown  = "subscription_currency_unknown"
	ReasonSubscriptionGone = "subscription_gone"
)

// Plan is a sync's decision.
type Plan struct {
	Action Action
	Amount int64
	Reason string
}

// Available is the credit still active at renewsAt, in cents: a credit that
// expires before the next order cannot pay it.
func Available(credits []state.AccountCredit, renewsAt time.Time) int64 {
	var total int64
	for _, c := range credits {
		if c.CentsRemaining <= 0 {
			continue
		}
		if c.ExpiresAt != nil && !renewsAt.IsZero() && !c.ExpiresAt.After(renewsAt) {
			continue
		}
		total += c.CentsRemaining
	}
	return total
}

// Decide picks the action for a subscription. current is Gregale's attached
// discount the subscription carries, nil when none; foreign reports that it
// carries a discount Gregale did not create.
func Decide(available int64, target billing.CreditDiscountTarget, current *state.CreditDiscount, foreign bool) Plan {
	switch {
	case foreign:
		return Plan{Action: Hold, Reason: ReasonForeignDiscount}
	case !target.Accepting:
		return Plan{Action: Hold, Reason: ReasonNotAccepting}
	case available <= 0 && current != nil:
		return Plan{Action: Detach}
	case available <= 0:
		return Plan{Action: Keep}
	case len(target.Currency) != 3:
		return Plan{Action: Hold, Reason: ReasonCurrencyUnknown}
	case current != nil && current.AmountCents == available && current.Currency == target.Currency:
		return Plan{Action: Keep}
	default:
		return Plan{Action: Attach, Amount: available}
	}
}
