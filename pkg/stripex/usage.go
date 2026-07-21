package stripex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
)

// Sentinel errors for the pre-SDK guard failures. The classifier in
// errors.go uses errors.Is to map these to stable Prometheus labels
// instead of string-fragment matching — adding a sentinel is the
// supported way to introduce a new pre-SDK failure mode.
var (
	// ErrNoAPIKey is returned when pushUsageRecordSDK is called
	// without an apiKey. Operators see this as the meterd boot-time
	// "STRIPE_API_KEY is empty" warning; the classifier surfaces it
	// as `result="no-api-key"` so the dashboard distinguishes a
	// misconfigured deployment from a live Stripe-side failure.
	ErrNoAPIKey = errors.New("stripex: cannot push usage without apiKey")

	// ErrNegativeQuantity is the defensive guard against a
	// negative GB-hour quantity that would silently credit the
	// customer. meterd never produces these; the gate documents
	// the invariant for future callers.
	ErrNegativeQuantity = errors.New("stripex: negative usage quantity")
)

// pushUsageRecordSDK is the seam where stripe-go lands (issue #52).
//
// It posts a metered UsageRecord against the per-account
// subscription_item ID that EnsureCustomer stamps on the account row
// via StripeCustomerSubscriptionCreated. The dedupe gate at
// (Client).PushUsageRecord already short-circuits repeated hours, so
// this function only runs once per (account, hour).
//
// Money is in spec-mandated integer cents/millicents per GB-h. The
// spec doesn't require sub-cent precision here, so we send
// millicents-of-GB — i.e. the wire quantity is
// int64(gbHours * 1000) — and Stripe aggregates at the source-currency
// scale (DecimalFractionDigits = 3 for USD).
//
// Idempotency is enforced with the standard Stripe pattern: one
// Idempotency-Key header derived from (accountID, RFC3339 hour). A
// redelivered meterd tick at the same hour generates the same key and
// Stripe replays the cached response rather than double-billing.
func (c *Client) pushUsageRecordSDK(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) error {
	_, err := c.pushUsageRecordSDKWithID(ctx, acct, hour, gbHours)
	return err
}

// pushUsageRecordSDKWithID is the SDK-touching implementation that
// returns the Stripe usage record on success. pushUsageRecordSDK
// (above) discards the record; the exported PushUsageRecordWithID
// surfaces it for the §14 M7 acceptance gate (issue #52).
func (c *Client) pushUsageRecordSDKWithID(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) (*stripe.UsageRecord, error) {
	if acct.StripeSubscriptionItem == "" {
		// Mirror the StripeCustomerID-emptiness skip at
		// client.go::PushUsageRecord — pending customers are a no-op.
		// products.go::EnsureCustomer stamps this on the first
		// successful subscription webhook.
		return nil, nil
	}
	if c.api == nil {
		// Without a constructed *client.API (no apiKey supplied), we'd
		// call UsageRecords.New against the unauthenticated `*Backend`
		// and bounce 401 off every push. Better to skip and surface the
		// misconfiguration in the meterd log line.
		return nil, fmt.Errorf("%w (account %s)", ErrNoAPIKey, acct.ID)
	}
	qty := int64(gbHours * 1000)
	if qty < 0 {
		// Defensive: a negative quantity would silently credit the
		// customer. meterd never produces these; the gate here
		// documents the invariant for future callers.
		return nil, fmt.Errorf("%w (account %s, qty %d)", ErrNegativeQuantity, acct.ID, qty)
	}
	idem := acct.ID + "/" + hour.UTC().Format(time.RFC3339)
	params := &stripe.UsageRecordParams{
		SubscriptionItem: stripe.String(acct.StripeSubscriptionItem),
		Quantity:         stripe.Int64(qty),
		Timestamp:        stripe.Int64(hour.UTC().Unix()),
		Action:           stripe.String(stripe.UsageRecordActionIncrement),
	}
	params.IdempotencyKey = stripe.String(idem)

	record, err := c.api.UsageRecords.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripex: UsageRecords.New account %s hour %s: %w", acct.ID, hour.UTC().Format(time.RFC3339), err)
	}
	return record, nil
}
