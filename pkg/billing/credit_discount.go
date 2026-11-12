package billing

import (
	"context"
	"time"
)

// CreditDiscountTarget is what a provider reports about the subscription a
// credit discount would go on (ADR-20261104212028).
type CreditDiscountTarget struct {
	// Accepting is false while the provider refuses a discount change: the
	// subscription is not active, ends at the period end, or has a plan
	// change pending.
	Accepting bool
	// DiscountID is the discount on the subscription now, "" when none.
	DiscountID string
	// Currency is the subscription's lower-case ISO 4217 currency.
	Currency string
	// RenewsAt is the end of the current period, when the next order is
	// created.
	RenewsAt time.Time
}

// CreditDiscounter applies account credit as a one-time fixed discount on a
// subscription's next order, instead of refunding a paid order
// (ADR-20261104212028). A discount reduces the whole order before tax.
type CreditDiscounter interface {
	// CreditDiscountTarget reads the subscription.
	CreditDiscountTarget(ctx context.Context, subscriptionID string) (CreditDiscountTarget, error)
	// CreateCreditDiscount creates a one-time fixed discount of amountCents in
	// currency, usable through the API only, and returns its id.
	CreateCreditDiscount(ctx context.Context, subjectID string, amountCents int64, currency string) (string, error)
	// SetSubscriptionDiscount puts discountID on the subscription, replacing
	// any discount there; "" removes it. It applies from the next order.
	SetSubscriptionDiscount(ctx context.Context, subscriptionID, discountID string) error
	// DeleteDiscount deletes a discount no subscription carries any more. A
	// discount already gone is not an error.
	DeleteDiscount(ctx context.Context, discountID string) error
}
