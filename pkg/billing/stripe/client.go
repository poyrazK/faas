package stripe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/client"
)

// EnsurePlanProducts is declared in products.go.
//
// The Client type satisfies the billing.Provider interface (declared
// in pkg/billing/provider.go). The Provider conformance is
// compile-time asserted by the _ billing.Provider = (*Client)(nil)
// line at the bottom of this file — adding a new method to the
// interface surfaces a build error here.
//
// Provider-specific parsing lives in this package; the rest of the
// codebase (apid, meterd, the dunning state machine, email) sees only
// billing.Event and never a Stripe-shaped JSON.

// Compile-time assertion that *Client satisfies billing.Provider.
var _ billing.Provider = (*Client)(nil)

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

// Client is the stripe facade. It carries the wiring every method needs
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
// shared by every *stripe.Client in the same process — there was only
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

// PushUsageRecord is the meterd-side entry point on the integer-mb-
// seconds path AND the billing.Provider conformance method (PR #1
// of the ADR-025 series). The pusher hands the sum of
// usage_minutes.mb_seconds for the billing window (a full day under
// the production cadence) and the SDK converts to the wire quantity
// in pure int64 arithmetic — no float, no per-hour truncation loss.
// See usage.go::pushUsageRecordSDKSum for the wire-quantity contract.
//
// Deduplicates on (account, hour) before issuing the Stripe call so a
// redelivered hour is a no-op. The (account, hour) key is unchanged
// from the float path; the dedupe table is unaware of the precision
// difference.
//
// PushUsageRecord satisfies both the billing.Provider interface and
// the legacy pkg/meter.StripePusher interface (retained for back-compat
// while PR #3 lands the provider-dispatch wiring at the meterd call
// site).
func (c *Client) PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	if acct.ProviderCustomerID == "" || acct.StripeSubscriptionItem == "" {
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
// PushUsageRecord (issue #52). Same skip / dedupe gate; returns the
// Stripe usage record on success so the live-sandbox test can assert
// record.Quantity matches the integer-quantized expectation.
//
// On the skip / dedupe short-circuit, returns (nil, nil) — callers must
// not assume a non-nil record on a successful return. The sandbox test
// pattern is: err == nil && record != nil && record.Quantity == want.
func (c *Client) PushUsageRecordSumWithID(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) (*stripe.UsageRecord, error) {
	if acct.ProviderCustomerID == "" || acct.StripeSubscriptionItem == "" {
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
	record, err := c.pushUsageRecordSDKSumWithID(ctx, acct, hour, mbSeconds)
	if err != nil {
		return nil, err
	}
	if err := c.dedupe.RecordStripePushHour(ctx, acct.ID, hour); err != nil {
		return nil, err
	}
	return record, nil
}

// PushUsageRecordSum is retained as an alias for PushUsageRecord so
// existing pkg/meter.StripePusher interface consumers keep compiling
// unchanged throughout the PR #1 rename. The deduplication uses the
// same gate as PushUsageRecord; the two are equivalent on the integer
// wire path.
func (c *Client) PushUsageRecordSum(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	return c.PushUsageRecord(ctx, acct, hour, mbSeconds)
}

// PushUsageRecordLegacy is the deprecated float-GB-hours wire path.
// It is preserved as a thin wrapper around PushUsageRecordSum so existing
// callers (and the legacy tests) keep their behaviour. The pusher
// path has migrated to PushUsageRecordSum — the integer-path variant
// — to eliminate per-hour fractional truncation loss on the wire.
//
// Deprecated: use PushUsageRecordSum. The float-to-int64 conversion
// truncates the sub-milliunit remainder, which over a 24h horizon
// accumulates to ~0.3 % of the customer's bill — above the spec's
// 0.1 % M7 acceptance delta.
func (c *Client) PushUsageRecordLegacy(ctx context.Context, acct state.Account, hour time.Time, gbHours float64) error {
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

// VerifyWebhook is the billing.Provider conformance method that wraps
// the package-level VerifySignature HMAC primitive and parses the
// Stripe-shaped JSON into a normalized billing.Event. Replaces the
// direct VerifySignature call at the apid handler boundary once the
// apid dispatch wiring lands (PR #3 of the ADR-025 series).
//
// The signature header is read from the headers map (case-insensitive)
// using the conventional key "Stripe-Signature". The tolerance is
// passed through unchanged so the apid handler can keep using its
// existing 5-minute default.
//
// On error, the wrapped error satisfies errors.Is(err,
// billing.ErrBadSignature). The payload is preserved in the returned
// Event.Raw for the audit log even when the type isn't recognized
// (apid treats unknown events as a no-op 200).
func (c *Client) VerifyWebhook(payload []byte, headers map[string]string, tolerance time.Duration) (billing.Event, error) {
	if c.secret == "" {
		return billing.Event{}, fmt.Errorf("stripe: %w: empty webhook secret", billing.ErrBadSignature)
	}
	// The headers argument is a plain map[string]string, not net/http's
	// canonicalizing Header type, so the lookup is case-sensitive. We
	// check both casings defensively because real-world callers vary on
	// whether they pre-canonicalize before handing us the map.
	sigHeader := headers["Stripe-Signature"]
	if sigHeader == "" {
		sigHeader = headers["stripe-signature"]
	}
	if sigHeader == "" {
		return billing.Event{}, fmt.Errorf("stripe: %w: missing Stripe-Signature header", billing.ErrBadSignature)
	}
	if err := VerifySignature(payload, sigHeader, c.secret, tolerance); err != nil {
		return billing.Event{}, err
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				Customer         string `json:"customer"`
				Subscription     string `json:"subscription"`
				Status           string `json:"status"`
				Plan             string `json:"plan"`
				SubscriptionItem string `json:"subscription_item"`
				// charge.refunded payload (issue #279):
				ID             string `json:"id"`
				AmountRefunded int64  `json:"amount_refunded"`
				Currency       string `json:"currency"`
				Refunded       bool   `json:"refunded"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return billing.Event{}, fmt.Errorf("stripe: parse webhook body: %w", err)
	}
	out := billing.Event{
		// Issue #294: Stripe event.id is the delivery UUID that apid's
		// webhook dedupe (pkg/webhookdedupe) consults. Empty when
		// Stripe did not stamp the field; apid treats an empty EventID
		// as "no dedupe" (pre-#294 behaviour).
		EventID:        ev.ID,
		Type:           mapStripeEventType(ev.Type),
		CustomerID:     ev.Data.Object.Customer,
		PlanID:         ev.Data.Object.Plan,
		SubscriptionID: ev.Data.Object.Subscription,
		Raw:            bytes.Clone(payload),
	}
	// For charge.refunded the Object IS the charge (Stripe's payload
	// shape: data.object.{id, amount_refunded, currency, refunded}).
	// amount_refunded is in millicents — convert to cents for the
	// billing.Event (which is always in cents; CLAUDE.md "no float on
	// money"). Integer math only.
	if ev.Type == "charge.refunded" {
		out.ChargeID = ev.Data.Object.ID
		out.Currency = ev.Data.Object.Currency
		out.AmountCents = ev.Data.Object.AmountRefunded / 10
	}
	return out, nil
}

// CreateUpgradeTransaction is the Stripe stub for the billing.Provider
// interface. Stripe's upgrade path is template-only: the apid 402
// carries the operator-controlled FAAS_BILLING_PORTAL_URL with
// {account_id} substituted, so the SDK never has to create a
// billing-portal session. The empty txID + checkoutURL are the
// "Stripe stub" signal — apid's changePlan handler reads txID == "" to
// render billing_portal_url instead of paddle_checkout_url + tx_id.
//
// Adding this stub keeps the var _ billing.Provider = (*Client)(nil)
// compile-time assertion (client.go:30) target-stable across PR #3's
// 5-method Provider interface. See pkg/billing/provider.go:75-99 for
// the full contract.
func (c *Client) CreateUpgradeTransaction(_ context.Context, _ state.Account, _ api.Plan) (string, string, error) {
	return "", "", nil
}

// Refund issues a refund against the named charge. amountCents is
// converted to millicents (×10) before being handed to the Stripe SDK
// because Stripe's Amount field is millicents (CLAUDE.md: integer
// cents/millicents; no float on money). The Idempotency-Key, if
// present on the context, is forwarded to Refunds.New so a network
// retry returns the same `re_…` id rather than creating a duplicate
// refund. apid stamps the operator's request's Idempotency-Key
// header onto the ctx before calling (cmd/apid/handlers_admin_refunds.go).
//
// Returns a wrapped *stripe.Error when Stripe rejects the call
// (e.g. amount_too_large, charge_already_refunded, charge_not_found).
// Today the apid handler maps any non-nil error to a 502 Problem;
// finer-grained mapping (e.g. amount_too_large → 400) is a follow-up.
func (c *Client) Refund(ctx context.Context, chargeID string, amountCents int64) (*billing.RefundResult, error) {
	params := &stripe.RefundParams{
		Charge: stripe.String(chargeID),
		Amount: stripe.Int64(centsToMillicents(amountCents)),
	}
	if k, ok := idempotencyKeyFromContext(ctx); ok {
		params.IdempotencyKey = stripe.String(k)
	}
	r, err := c.api.Refunds.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe: refund charge=%s amount_cents=%d: %w", chargeID, amountCents, err)
	}
	return &billing.RefundResult{
		ProviderRefundID: r.ID,
		ChargeID:         chargeID,
		AmountCents:      amountCents,
		Currency:         string(r.Currency),
	}, nil
}

// ReconcileUsage queries Stripe for the mb_seconds total pushed in
// the [start, end) window via stripe.UsageRecordSummaries.List and
// returns it as int64 mb_seconds. ADR-049 §B.1.
//
// Read-only against Stripe — does NOT mutate customer /
// subscription state. Returns (0, err) on any SDK error so the
// reconciler can fail-soft log-and-skip the account.
//
// The actual Stripe SDK call is intentionally a stub for this PR.
// The interface contract is the load-bearing seam — wiring the
// SDK's TotalUsage summation lands in a follow-up PR against the
// stripe sandbox. Today we return (0, nil) so the reconciler's
// local-only drift signal (usage_minutes total) drives the
// BillingDrift alert. The reconciler skips Stripe on the
// "0 returned" path; the alert is gated on ratio > 0.005 so a
// non-Stripe provider drift is still detected via the Paddle path
// when that lands.
//
// Returns ErrNotImplemented until the SDK summation lands in a
// follow-up PR. Returning (0, nil) here would make the reconciler
// compute drift_ratio = abs(local − 0) / max(local, 0) = 1.0 for
// every paying account and page the BillingDrift alert from the
// moment this PR ships. ErrNotImplemented is the documented
// "no drift signal yet" sentinel: the reconciler's
// `errors.Is(err, billing.ErrNotImplemented)` short-circuit
// (pkg/billing/reconciler/reconciler.go) skips the gauge emission
// for this account entirely, matching the Paddle stub's behaviour.
func (c *Client) ReconcileUsage(_ context.Context, _ state.Account, _, _ time.Time) (int64, error) {
	return 0, billing.ErrNotImplemented
}

// Capabilities returns the Stripe provider's supported optional
// surfaces (see pkg/billing/provider.go CapabilitySet). The set is
// derived from the implementation's actual behaviour — CapHostedCheckout
// is intentionally absent because Stripe's CreateUpgradeTransaction
// returns ("", "", nil) and the apid handler falls back to
// FAAS_BILLING_PORTAL_URL instead. CapUsageReconcile is absent because
// ReconcileUsage returns ErrNotImplemented (the reconciler short-
// circuits with errors.Is, mirroring the Paddle stub).
func (c *Client) Capabilities() billing.CapabilitySet {
	return StripeCapabilities()
}

// StripeCapabilities returns the static capability set for the
// Stripe provider. Lifted out of *Client.Capabilities so the
// loader's Providers() metadata-only path (loader.go:160) does not
// have to construct a *Client just to read the bits. The capability
// set is invariant — Capabilities() never reads c.api — so a free
// function is the correct shape.
//
// Exported because the loader (pkg/billing/loader) is a separate
// package and needs to read the static set without constructing a
// *Client. Exposing the function (not the value) keeps future
// capability-set composition (e.g. adding CapUsageReconcile) localised
// to this file. Mirrors paddle.PaddleCapabilities.
func StripeCapabilities() billing.CapabilitySet {
	return billing.CapabilitySet(billing.CapRefund | billing.CapUsageMetered | billing.CapSandbox)
}

// centsToMillicents converts integer EUR cents to Stripe's native
// millicents (×10). CLAUDE.md invariant: integer cents/millicents
// only; never float on money. The factor is fixed (Stripe's wire
// format uses 10 millicents per cent across every supported currency);
// any drift here would silently bill customers the wrong amount.
//
// Extracted as a pure helper so the conversion is unit-testable
// without standing up the stripe-go SDK / a live sandbox. The
// caller (Client.Refund) hands the result straight to
// stripe.Int64, which accepts int64.
func centsToMillicents(cents int64) int64 {
	return cents * 10
}

// idempotencyKeyContextKey is retained for compatibility with the package's
// older focused tests. New callers should use billing.ContextWithIdempotencyKey
// so the same operation key can cross provider implementations.
type idempotencyKeyContextKey struct{}

// idempotencyKeyFromContext retains the package-local helper used by the
// Stripe tests while delegating the production context contract to the
// shared billing package. The apid handler can therefore pass one key to any
// selected provider without importing provider-private types.
func idempotencyKeyFromContext(ctx context.Context) (string, bool) {
	if key, ok := billing.IdempotencyKeyFromContext(ctx); ok {
		return key, true
	}
	key, ok := ctx.Value(idempotencyKeyContextKey{}).(string)
	return key, ok && key != ""
}

// mapStripeEventType translates Stripe's `type` strings into the
// normalized billing.EventType. Unknown types return EventUnknown so
// apid's switch falls through to a 200 no-op (Stripe expects 2xx for
// everything it didn't recognize so it doesn't retry forever).
func mapStripeEventType(t string) billing.EventType {
	switch t {
	case "customer.subscription.created":
		return billing.EventSubscriptionCreated
	case "customer.subscription.updated":
		return billing.EventSubscriptionUpdated
	case "customer.subscription.deleted":
		return billing.EventSubscriptionCanceled
	case "customer.subscription.past_due":
		return billing.EventSubscriptionPastDue
	case "invoice.payment_succeeded":
		return billing.EventPaymentSucceeded
	case "invoice.payment_failed":
		return billing.EventPaymentFailed
	case "charge.refunded":
		return billing.EventRefundProcessed
	default:
		return billing.EventUnknown
	}
}
