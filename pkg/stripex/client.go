package stripex

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
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
type Client struct {
	store   state.Store
	dedupe  PushDedupe
	apiKey  string
	secret  string
	log     *slog.Logger
	now     func() time.Time
	// PlanPriceIDs is the lookup map EnsurePlanProducts populates and
	// EnsureCustomer reads. key = plan:price-kind (e.g. "hobby:monthly").
	PlanPriceIDs map[string]string
}

// NewClient wires the facade. apiKey + secret are read from the config;
// callers pass empty strings in tests.
func NewClient(store state.Store, dedupe PushDedupe, apiKey, secret string, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		store:        store,
		dedupe:       dedupe,
		apiKey:       apiKey,
		secret:       secret,
		log:          log,
		now:          time.Now,
		PlanPriceIDs: map[string]string{},
	}
}

// PushUsageRecord is the meterd-side entry point. It deduplicates on
// (account, hour) before issuing the Stripe call so a redelivered hour
// is a no-op. The Stripe call itself is gated behind a real-stripe
// interface (see TODO in usage.go); the unit tests exercise the dedupe
// gate end-to-end without the SDK.
//
// PushUsageRecord satisfies the pkg/meter.StripePusher interface.
func (c *Client) PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) error {
	if acct.StripeCustomerID == "" {
		// No customer yet — skip silently. The customer is created on
		// the first successful subscription webhook; until then there's
		// nothing to bill.
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
