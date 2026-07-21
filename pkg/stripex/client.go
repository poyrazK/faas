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

// PushUsageRecord is the meterd-side entry point. It deduplicates on
// (account, hour) before issuing the Stripe call so a redelivered hour
// is a no-op. The Stripe call itself is gated behind a real-stripe
// SDK call (see usage.go); the unit tests exercise the dedupe gate
// end-to-end without the SDK.
//
// PushUsageRecord satisfies the pkg/meter.StripePusher interface.
func (c *Client) PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) error {
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
	if err := c.pushUsageRecordSDK(ctx, acct, hour, gbHours); err != nil {
		return err
	}
	return c.dedupe.RecordStripePushHour(ctx, acct.ID, hour)
}

// EnsurePlanProducts is declared in products.go.

// PushUsageRecordWithID is the §14 M7 acceptance sibling to
// PushUsageRecord (issue #52). Same skip / dedupe gate; returns the
// Stripe usage record on success so the live-sandbox test can assert
// record.ID. PushUsageRecord keeps its (ctx, acct, hour, gbHours) error
// signature so pkg/meter.StripePusher is unchanged.
//
// On the skip / dedupe short-circuit, returns (nil, nil) — callers must
// not assume a non-nil record on a successful return. The sandbox test
// pattern is: err == nil && record != nil && record.ID != "".
func (c *Client) PushUsageRecordWithID(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) (*stripe.UsageRecord, error) {
	if acct.StripeCustomerID == "" || acct.StripeSubscriptionItem == "" {
		// Same skip as PushUsageRecord — pending customers are a no-op.
		return nil, nil
	}
	dup, err := c.dedupe.HasStripePushHour(ctx, acct.ID, hour)
	if err != nil {
		return nil, err
	}
	if dup {
		return nil, nil
	}
	record, err := c.pushUsageRecordSDKWithID(ctx, acct, hour, gbHours)
	if err != nil {
		return nil, err
	}
	if err := c.dedupe.RecordStripePushHour(ctx, acct.ID, hour); err != nil {
		return nil, err
	}
	return record, nil
}
