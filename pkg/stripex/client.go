package stripex

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/client"
)

// PushDedupe is the dedupe table that lets meterd's hourly loop push the
// same (account, hour) twice without double-billing. Both MemStore and
// PgStore implement this through the same Store interface methods.
type PushDedupe interface {
	// HasStripePushHour returns true if a usage record for (accountID, hour)
	// was already pushed. The caller skips the Stripe call when true.
	HasStripePushHour(ctx context.Context, accountID string, hour time.Time) (bool, error)
	// RecordStripePushHour stamps the dedupe row. Idempotent on a re-call
	// for the same hour.
	RecordStripePushHour(ctx context.Context, accountID string, hour time.Time) error
}

// Client is the stripex facade. It carries the wiring every method needs
// (state.Store + PushDedupe + api key + secret) and exposes the four
// methods M7 uses. The struct is intentionally tiny — every method is a
// primitive over a single stripe-go call, so testing can substitute a
// recording stub via the interfaces in this file.
//
// api is a typed per-call *client.API built once in NewClient when
// apiKey is non-empty. nil when apiKey == "" so the dev-loop no-key
// path keeps skipping every SDK call (mirrors the existing skip in
// pushUsageRecordSDK). Replaces the previous stripe.Key global mutation
// at usage.go which was process-global state (the package-level key is
// shared by every *stripex.Client in the same process — there was only
// ever one Client per meterd, but the *client.API field is the
// stripe-go-v70-blessed way to scope a key to a single Client and
// future-proofs against a second Client with a different key).
type Client struct {
	store  state.Store
	dedupe PushDedupe
	apiKey string
	secret string
	log    *slog.Logger
	now    func() time.Time
	// api is the typed stripe-go client (customer, plan, usagerecord
	// sub-clients pre-bound to the apiKey). Nil when apiKey == "".
	api *client.API
	// PlanPriceIDs is the lookup map EnsurePlanProducts populates and
	// EnsureCustomer reads. key = plan:price-kind (e.g. "hobby:monthly").
	PlanPriceIDs map[string]string
}

// NewClient wires the facade. apiKey + secret are read from the config;
// callers pass empty strings in tests. When apiKey is non-empty,
// constructs a per-call *client.API so subsequent SDK calls don't have
// to mutate the package-global stripe.Key.
func NewClient(store state.Store, dedupe PushDedupe, apiKey, secret string, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	c := &Client{
		store:        store,
		dedupe:       dedupe,
		apiKey:       apiKey,
		secret:       secret,
		log:          log,
		now:          time.Now,
		PlanPriceIDs: map[string]string{},
	}
	if apiKey != "" {
		c.api = client.New(apiKey, &stripe.Backends{
			API: stripe.GetBackend(stripe.APIBackend),
		})
	}
	return c
}

// PushUsageRecordSum is the meterd-side entry point on the integer-mb-
// seconds path. The pusher hands the sum of usage_minutes.mb_seconds
// for the billing window (a full day under the production cadence) and
// the SDK converts to the wire quantity in pure int64 arithmetic — no
// float, no per-hour truncation loss. See usage.go::pushUsageRecordSDKSum
// for the wire-quantity contract.
//
// Deduplicates on (account, hour) before issuing the Stripe call so a
// redelivered hour is a no-op. The (account, hour) key is unchanged
// from the float path; the dedupe table is unaware of the precision
// difference. PushUsageRecordSum satisfies the
// pkg/meter.StripePusher interface.
func (c *Client) PushUsageRecordSum(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	if acct.StripeCustomerID == "" || acct.StripeSubscriptionItem == "" {
		// No customer / subscription yet — skip silently. Either
		// field being empty means there's no Stripe surface to bill
		// against; the missing subscription_item case is the
		// "customer exists but products.go::EnsureCustomer hasn't
		// stamped the subscription.created webhook yet" interregnum.
		return nil
	}
	dup, err := c.dedupe.HasStripePushHour(ctx, acct.ID, hour)
	if err != nil {
		return err
	}
	if dup {
		return nil
	}
	if err := c.pushUsageRecordSDKSum(ctx, acct, hour, mbSeconds); err != nil {
		return err
	}
	return c.dedupe.RecordStripePushHour(ctx, acct.ID, hour)
}

// EnsurePlanProducts is declared in products.go.

// PushUsageRecordSumWithID is the §14 M7 acceptance sibling to
// PushUsageRecordSum (issue #52). Same skip / dedupe gate; returns the
// Stripe usage record on success so the live-sandbox test can assert
// record.Quantity matches the integer-quantized expectation.
//
// On the skip / dedupe short-circuit, returns (nil, nil) — callers must
// not assume a non-nil record on a successful return. The sandbox test
// pattern is: err == nil && record != nil && record.Quantity == want.
func (c *Client) PushUsageRecordSumWithID(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) (*stripe.UsageRecord, error) {
	if acct.StripeCustomerID == "" || acct.StripeSubscriptionItem == "" {
		// Same skip as PushUsageRecordSum — pending customers are a no-op.
		return nil, nil
	}
	dup, err := c.dedupe.HasStripePushHour(ctx, acct.ID, hour)
	if err != nil {
		return nil, err
	}
	if dup {
		return nil, nil
	}
	record, err := c.pushUsageRecordSDKSumWithID(ctx, acct, hour, mbSeconds)
	if err != nil {
		return nil, err
	}
	if err := c.dedupe.RecordStripePushHour(ctx, acct.ID, hour); err != nil {
		return nil, err
	}
	return record, nil
}

// PushUsageRecord is the legacy float-GB-hours wire path. It is
// preserved as a thin wrapper around PushUsageRecordSum so existing
// callers (and the legacy tests) keep their behaviour. The pusher
// path has migrated to PushUsageRecordSum — the integer-path variant
// — to eliminate per-hour fractional truncation loss on the wire.
//
// Deprecated: use PushUsageRecordSum. The float-to-int64 conversion
// truncates the sub-milliunit remainder, which over a 24h horizon
// accumulates to ~0.3 % of the customer's bill — above the spec's
// 0.1 % M7 acceptance delta.
func (c *Client) PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) error {
	// Float → mb_seconds then route through the integer path. The
	// per-call truncation is identical to the legacy code at the
	// SDK call site.
	mbSeconds := int64(gbHours * 1024 * 3600)
	return c.PushUsageRecordSum(ctx, acct, hour, mbSeconds)
}

// PushUsageRecordWithID is the legacy float-GB-hours wire path that
// returns the Stripe usage record. Thin wrapper around
// PushUsageRecordSumWithID.
//
// Deprecated: use PushUsageRecordSumWithID.
func (c *Client) PushUsageRecordWithID(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) (*stripe.UsageRecord, error) {
	mbSeconds := int64(gbHours * 1024 * 3600)
	return c.PushUsageRecordSumWithID(ctx, acct, hour, mbSeconds)
}
