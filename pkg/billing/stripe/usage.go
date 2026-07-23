package stripe

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
	// Integer wire quantity — no float on the path. The formula is
	// (mbSeconds * WireQuantityMillicentsPerGBHour) / secondsPerGBHour
	// evaluated in Go int64 arithmetic. Range guard: the largest
	// billable window under spec §4.7 is a 1 TB instance resident
	// for 24 h = ~2.1e9 mb_seconds, so mbSeconds * 1000 ≈ 2.1e12,
	// well below int64 max (~9.2e18). Truncation is by design — the
	// sub-milliunit remainder is dropped exactly the way the spec's
	// integer money model requires (CLAUDE.md: "Floats near money
	// fail review").
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
// (see pkg/billing/stripe/usage_test.go::TestPushUsageRecord_PostsToStripeSandbox
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
// Returns the Stripe usage record on success. The wire quantity
// mirrors the pre-M7 legacy formula exactly (`int64(gbHours * 1000)`,
// a per-call millicents-of-GB-h value with no further division) —
// preserved so existing callers and the legacy
// TestPushUsageRecord_PostsToStripeSandbox regression continue to
// produce bit-identical Stripe records. Note this is intentionally
// *not* the new `mbSeconds * 1000 / secondsPerGBHour` path; routing
// through mb_seconds would change the wire quantity on any input
// whose gb_hours * 1024 * 3600 is not a whole multiple of (1024 *
// 3600 / 1000) = 3686.4 — i.e. almost every realistic input.
func (c *Client) pushUsageRecordSDKWithID(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) (*stripe.UsageRecord, error) {
	if acct.StripeSubscriptionItem == "" {
		return nil, nil
	}
	if c.api == nil {
		// Same nil-api guard as pushUsageRecordSDKSumWithID — see
		// that function's comment for why we'd rather surface a
		// clear error than bounce 401s off the unauthenticated SDK.
		return nil, fmt.Errorf("%w (account %s)", ErrNoAPIKey, acct.ID)
	}
	// Legacy wire formula. Truncates the sub-milliunit remainder —
	// the very truncation the M7 fix avoids on the integer path.
	// Keep this exactly as-is so callers on the float path see no
	// behaviour change.
	qty := int64(gbHours * WireQuantityMillicentsPerGBHour)
	if qty < 0 {
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
