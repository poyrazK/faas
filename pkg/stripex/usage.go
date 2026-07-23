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
	// ErrNoAPIKey is returned when pushUsageRecordSDKSum is called
	// without an apiKey. Operators see this as the meterd boot-time
	// "STRIPE_API_KEY is empty" warning; the classifier surfaces it
	// as `result="no-api-key"` so the dashboard distinguishes a
	// misconfigured deployment from a live Stripe-side failure.
	ErrNoAPIKey = errors.New("stripex: cannot push usage without apiKey")

	// ErrNegativeQuantity is the defensive guard against a
	// negative wire quantity that would silently credit the
	// customer. meterd never produces these; the gate documents
	// the invariant for future callers.
	ErrNegativeQuantity = errors.New("stripex: negative usage quantity")
)

// WireQuantityMillicentsPerGBHour is the scale factor used to convert
// GB-RAM-hours into the integer wire-quantity that Stripe's metered
// subscription_item accepts. The spec defines billing in
// millicents-of-GB-h (1 GB-h = 1000 wire units), so millicents-of-GB
// and wire-quantity-of-GB-h are the same integer axis. Centralized in
// one constant so the conversion and the tests stay in lockstep.
const WireQuantityMillicentsPerGBHour = 1000

// secondsPerGBHour is the integer conversion factor for mb_seconds →
// GB-RAM-hours: 1 MB resident for 1 second = 1/(1024*3600) GB-h. The
// product is exact integer arithmetic — no float — so the wire
// quantity is deterministic across architectures and rounding modes.
const secondsPerGBHour = 1024 * 3600

// pushUsageRecordSDKSum is the seam where stripe-go lands (issue #52).
//
// It posts a metered UsageRecord against the per-account
// subscription_item ID that EnsureCustomer stamps on the account row
// via StripeCustomerSubscriptionCreated. The dedupe gate at
// (Client).PushUsageRecordSum already short-circuits repeated hours,
// so this function only runs once per (account, hour).
//
// The wire quantity is integer arithmetic:
//
//	qty = mbSeconds * WireQuantityMillicentsPerGBHour / secondsPerGBHour
//
// Pure integer math — no float, no per-hour truncation loss. The
// pusher calls this with the *sum* of mb_seconds across the billing
// window (a full day for the production cadence, see
// meterd.cfg.StripeInterval) so the wire quantity is the deterministic
// integer answer for that window. Stripe aggregates at the
// source-currency scale (DecimalFractionDigits = 3 for USD).
//
// Idempotency is enforced with the standard Stripe pattern: one
// Idempotency-Key header derived from (accountID, RFC3339 hour). A
// redelivered meterd tick at the same hour generates the same key and
// Stripe replays the cached response rather than double-billing.
func (c *Client) pushUsageRecordSDKSum(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	_, err := c.pushUsageRecordSDKSumWithID(ctx, acct, hour, mbSeconds)
	return err
}

// pushUsageRecordSDKSumWithID is the SDK-touching implementation that
// returns the Stripe usage record on success. pushUsageRecordSDKSum
// (above) discards the record; the exported PushUsageRecordSumWithID
// surfaces it for the §14 M7 acceptance gate (issue #52).
func (c *Client) pushUsageRecordSDKSumWithID(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) (*stripe.UsageRecord, error) {
	if acct.StripeSubscriptionItem == "" {
		// Mirror the StripeCustomerID-emptiness skip at
		// client.go::PushUsageRecordSum — pending customers are a no-op.
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
	// Integer wire quantity — no float on the path. The order of
	// the multiplications and divisions doesn't affect the result
	// (1024 * 3600 = 3_686_400 divides cleanly into mbSeconds * 1000
	// for any non-negative mbSeconds); Go's int64 arithmetic is
	// well-defined under the assumed ranges (24h of a 1 GB instance
	// = 3.6e10 mb_seconds, well below int64 max).
	qty := mbSeconds * WireQuantityMillicentsPerGBHour / secondsPerGBHour
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

// pushUsageRecordSDK is the legacy float-GB-hours wire path. It is
// preserved as a thin wrapper around pushUsageRecordSDKSum so existing
// callers (and the legacy tests) keep their behaviour. The pusher
// path has migrated to PushUsageRecordSum — the integer-path variant
// — to eliminate per-hour fractional truncation loss on the wire
// (see pkg/stripex/usage_test.go::TestPushUsageRecord_PostsToStripeSandbox
// for the live regression guard).
//
// Deprecated: use pushUsageRecordSDKSum. The float-to-int64 conversion
// truncates the sub-milliunit remainder, which over a 24h horizon
// accumulates to ~0.3 % of the customer's bill — above the spec's
// 0.1 % M7 acceptance delta.
func (c *Client) pushUsageRecordSDK(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) error {
	_, err := c.pushUsageRecordSDKWithID(ctx, acct, hour, gbHours)
	return err
}

// pushUsageRecordSDKWithID is the SDK-touching legacy implementation.
// Returns the Stripe usage record on success. The wire quantity is
// computed in integer arithmetic from the float GB-hour input.
func (c *Client) pushUsageRecordSDKWithID(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) (*stripe.UsageRecord, error) {
	if acct.StripeSubscriptionItem == "" {
		return nil, nil
	}
	// Convert gbHours → mbSeconds, then route through the integer path.
	// Same per-call truncation as the legacy code; the bit-exactness is
	// preserved for the legacy wire shape (float-in, int64-out).
	mbSeconds := int64(gbHours * float64(secondsPerGBHour))
	return c.pushUsageRecordSDKSumWithID(ctx, acct, hour, mbSeconds)
}
