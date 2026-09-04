// Package billing is the per-deployment abstraction over external payment
// processors (ADR-025). One Provider is selected at boot from
// FAAS_BILLING_PROVIDER; the selected implementation is the only registered
// call site for whatever vendor SDK we use, so the surface stays
// audit-friendly.
//
// The interface is intentionally narrow — the four primitives M7 needs —
// so adding a third provider is a one-package PR. Concrete
// implementations today:
//
//   - pkg/billing/stripe — extracted from the original pkg/stripex package.
//     Legacy opt-in via FAAS_BILLING_PROVIDER=stripe.
//   - pkg/billing/paddle — Paddle Billing v2 (current API). Explicit opt-in
//     via FAAS_BILLING_PROVIDER=paddle.
//   - pkg/billing/polar — Polar MoR REST API + event-based usage billing.
//     Public-release default and explicit option via FAAS_BILLING_PROVIDER=polar.
//
// Provider-specific behaviour stays inside each implementation; the rest
// of the codebase (apid, meterd, the dunning state machine, the email
// surface) talks only to the Provider interface.
package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Provider is the per-deployment abstraction apid and meterd use. The
// selected implementation handles every external billing call (product
// setup, customer creation, hourly usage push, webhook verification).
// apid's webhook handler dispatches to the same Provider after a
// successful VerifyWebhook call.
//
// Implementations MUST be safe for concurrent use; meterd's quota + dunning
// loops and apid's webhook ingress call into Provider from multiple
// goroutines.
//
// Implementations declare which optional surfaces they expose via
// Capabilities() (CapabilitySet). Callers SHOULD check the capability
// bit before dispatching onto a provider-specific code path (e.g.
// render the hosted-checkout URL only when CapHostedCheckout is set);
// the runtime fallback is the previous sentinel-style contract —
// CreateUpgradeTransaction returning ("", "", nil) means no hosted
// checkout, while methods returning ErrNotImplemented mean the provider
// doesn't support the surface. The two co-exist so adding Capabilities() to existing
// implementations is a non-breaking change.
type Provider interface {
	// Capabilities returns the bitmask of optional surfaces this
	// implementation exposes. The set is stable for the life of the
	// provider — call once at boot and cache, or query on every
	// dispatch if cheap. See Capability / CapabilitySet below.
	Capabilities() CapabilitySet

	// EnsurePlanProducts is the idempotent product/price setup at boot.
	// Stripe: stripe.Plans.List + stripe.Plans.New by Nickname. Paddle:
	// paddle.Items.List + paddle.Items.Create by description match.
	// Idempotent across restarts so a redelivered boot is a no-op.
	EnsurePlanProducts(ctx context.Context) error

	// CreateCustomer maps a state.Account to the provider's customer
	// handle and writes the ID back onto the account row. The column
	// the ID lands in is named after the Stripe-only era today; a
	// column rename is out of scope for ADR-025 (separate, smaller
	// migration PR).
	CreateCustomer(ctx context.Context, acct state.Account) (string, error)

	// PushUsageRecord is the meterd pusher. Stripe: per-hour metered
	// UsageRecord against the customer's subscription item. Paddle:
	// at month-rollover, posts a flat-rate line item for the prior
	// month's accumulated mb_seconds; non-rollover calls accumulate
	// internally.
	//
	// Signature is symmetric so meterd's loop is implementation-agnostic.
	// The dedupe contract (a redelivered hour is a no-op) is the
	// implementation's responsibility — implementations should
	// idempotency-key every external call against (acct.ID, hour-or-month).
	PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error

	// VerifyWebhook checks a provider-shaped signature header against
	// the body and returns a normalized Event. apid then matches the
	// Event against the dunning state machine — apid never sees
	// provider-shaped JSON.
	//
	// tolerance caps the timestamp window (Stripe: Stripe-Signature `t=`;
	// Paddle: Paddle-Signature `ts=`). Empty header / bad signature
	// returns ErrBadSignature, wrapped with operation context.
	VerifyWebhook(payload []byte, headers map[string]string, tolerance time.Duration) (Event, error)

	// CreateUpgradeTransaction materializes the provider's hosted-checkout
	// surface for an upgrade to targetPlan. apid's changePlan handler
	// calls this when an account's plan is being upgraded and the
	// customer has no active subscription item yet — the typical
	// free → paid direct path (spec §4.7).
	//
	//   - Hosted-checkout providers return a provider checkout handle and
	//     URL. The 402 Problem carries the URL in the provider-neutral
	//     CheckoutURL field and retains legacy provider-specific aliases
	//     where required by older clients.
	//   - Providers without hosted checkout return ("", "", nil), which
	//     tells apid to use a provider portal session or the configured
	//     FAAS_BILLING_PORTAL_URL fallback.
	//
	// The "(txID == \"\") ⇒ no hosted checkout" contract is the dispatch
	// signal the apid handler branches on. Implementations should add a stable
	// Idempotency-Key (Paddle: recorded in CustomData) so a redelivered
	// upgrade click doesn't create a duplicate Transaction.
	CreateUpgradeTransaction(ctx context.Context, acct state.Account, targetPlan api.Plan) (txID, checkoutURL string, err error)

	// ReconcileUsage is the read-only drift detector (ADR-049 §B.1).
	// meterd's pkg/billing/reconciler runs every
	// FAAS_RECONCILE_INTERVAL and asks each implementation how
	// many mb_seconds it has actually pushed for the given hour
	// window. The reconciler compares that sum against the local
	// provider quantity and exposes the diff as Prometheus gauges +
	// the BillingDrift alert. Providers implementing
	// UsageModeProvider may use net calendar-month overage instead of
	// raw local usage.
	//
	// Read-only against the provider — implementations MUST NOT
	// mutate customer / subscription state from this call.
	// Failure mode: the implementation should return (0, err)
	// on network / 5xx / rate-limit and let the reconciler
	// log-and-skip the account (the FAAS_BILLING_PROVIDER-
	// specific quota costs live inside the implementation, not
	// the interface).
	//
	// Stripe: stripe.UsageRecordSummaries.list(subscription_item,
	// start, end) summed. Paddle: Paddle Billing does not yet
	// expose a usage-summary endpoint, so the Paddle
	// implementation returns ErrNotImplemented until the upstream
	// adds one. Polar: the configured meter quantities endpoint is
	// summed when FAAS_POLAR_METER_ID is set; without a meter ID it
	// returns ErrNotImplemented for direct callers (startup validation
	// rejects that configuration).
	ReconcileUsage(ctx context.Context, acct state.Account, start, end time.Time) (pushedMBSeconds int64, err error)

	// Refund issues an operator-initiated refund against a charge
	// (issue #279). amountCents is integer cents (the financial
	// model's unit; never float on money). The implementation is
	// responsible for:
	//
	//   1. Translating cents → the provider's native unit (Stripe:
	//      millicents; Paddle: adjustment line-item cents).
	//   2. Forwarding a ctx-derived Idempotency-Key so a network-blip
	//      retry does not create a duplicate refund. Stripe: read
	//      via idempotencyKeyFromCtx; Paddle: ContextWithTransitID).
	//   3. Mapping provider errors (Stripe: amount_too_large,
	//      charge_already_refunded, etc.) onto errors the apid
	//      handler can dispatch on. Today the handler returns
	//      502 for any non-nil error; the refinement is a follow-up.
	//
	// apid's operator route is the caller
	// (cmd/apid/handlers_admin_refunds.go). The webhook (charge.refunded or
	// the provider-equivalent refund event) is observational and routes
	// through VerifyWebhook → EventRefundProcessed, NOT through this
	// method.
	Refund(ctx context.Context, chargeID string, amountCents int64) (*RefundResult, error)

	// RetryLatestCharge materializes a new charge attempt against
	// the account's saved card where the provider exposes that operation
	// (issue #242). Returns the new attempt's id + the provider's reference
	// id; the apid handler echoes them in BillingRetryResponse. Providers
	// without a direct retry surface return ErrNotImplemented and apid
	// directs the customer to the billing portal.
	//
	//   - Stripe: walks the customer's open invoices in reverse
	//     chronological order and calls Invoices.Pay on the latest
	//     one. Idempotency-Key is derived from
	//     `acct.ID + "/retry/" + invoice.ID` so a flaky-network
	//     redelivery collapses to one Stripe-side attempt.
	//   - Paddle: posts a fresh CreateTransaction against the
	//     existing customer (ctm_…) for the same plan's monthly
	//     price plus month-to-date overage. Idempotency-Key is
	//     pinned via paddle.ContextWithTransitID; CustomData
	//     stamps kind=billing_retry so the merchant dashboard
	//     distinguishes it from a fresh upgrade.
	//
	// Sentinel errors:
	//   - ErrNoOpenCharge — no open invoice / transaction to retry
	//     (account in good standing; apid maps to 404).
	//   - ErrNoAPIKey — provider SDK not constructed (operator
	//     mis-config; apid maps to 502 + clear hint).
	RetryLatestCharge(ctx context.Context, acct state.Account) (attemptID, providerRefID string, err error)

	// CancelAtPeriodEnd sets cancel_at_period_end on the account's
	// subscription (issue #242). Account keeps running until period
	// end, then downgrades to Free (spec §4.7).
	//
	//   - Stripe: Subscriptions.Update(cancel_at_period_end=true).
	//     The flag lives on Stripe-side; no local mirror. Returns
	//     sub.CurrentPeriodEnd as the effective timestamp.
	//   - Paddle: Subscriptions.Cancel with
	//     EffectiveFromNextBillingPeriod. Returns the effective date
	//     from Paddle's scheduled-change/current-period response.
	//
	// Sentinel errors:
	//   - ErrAlreadyCancelled — no active subscription (Free / post-
	//     cancel / never-checked-out); apid maps to 409 + clear
	//     hint. Stripe returns this on no-subscription; Stripe's
	//     re-cancel of an already-cancelled sub is idempotent and
	//     returns 200 (so we don't return this on re-cancel).
	CancelAtPeriodEnd(ctx context.Context, acct state.Account) (effectiveAt time.Time, err error)

	// PaymentMethodSummary returns the card-on-file summary for the
	// account (issue #242). The zero PaymentMethod is the "no card
	// on file" sentinel — apid's handler omits the wire-DTO field
	// in that case so the response stays clean.
	//
	//   - Stripe: PaymentMethods.List against the customer's
	//     customer ID, reduced to (brand, last4, exp_month,
	//     exp_year).
	//   - Paddle: Customer.Get parses the customer's stored
	//     PaymentMethod block. Paddle exposes the card-on-file on
	//     the Customer object itself.
	//
	// Implementations MUST NOT fail when the customer has no card
	// on file — they return the zero PaymentMethod, not an error.
	// The only error path is the provider-SDK failure case
	// (ErrNoAPIKey on Stripe when no SDK is constructed).
	PaymentMethodSummary(ctx context.Context, acct state.Account) (PaymentMethod, error)
}

// UsageModeProvider is an optional provider contract for usage semantics.
// Providers that return UsageModeOverage receive only the portion above
// Gregale's included calendar-month quota. Providers without this optional
// surface retain the historical raw-usage contract.
type UsageModeProvider interface {
	UsageMode() UsageMode
}

// UsageMode describes the quantity passed to PushUsageRecord and returned by
// ReconcileUsage.
type UsageMode string

const (
	// UsageModeRaw is the legacy provider contract: the quantity is all local
	// usage in the requested window.
	UsageModeRaw UsageMode = "raw"
	// UsageModeOverage is the calendar-month net-overage contract. The
	// included plan allowance is removed locally before provider ingestion.
	UsageModeOverage UsageMode = "overage"
)

// CustomerPortalProvider is an optional provider surface for creating a
// short-lived, customer-authenticated billing portal session. Providers that
// do not expose a session API can continue using the operator-configured
// BillingPortalURL fallback.
type CustomerPortalProvider interface {
	CreateCustomerPortalSession(ctx context.Context, acct state.Account, returnURL string) (portalURL string, err error)
}

// SubscriptionPlanChangeProvider is an optional provider surface for
// scheduling a change to an existing subscription. The caller must not
// change the local entitlement before the provider confirms the new product
// through a webhook. Providers that do not expose subscription product
// changes leave customers on the hosted billing portal path.
//
// targetPlan == api.PlanFree means cancel the subscription at period end;
// paid-to-paid changes should use the provider's non-prorating or
// next-period semantics unless the provider explicitly documents otherwise.
type SubscriptionPlanChangeProvider interface {
	ChangeSubscriptionPlan(ctx context.Context, acct state.Account, targetPlan api.Plan) (effectiveAt time.Time, err error)
}

// InvoicePDFRequester is an optional provider surface for requesting an
// invoice PDF after a paid order. Providers whose invoices are generated
// automatically need not implement it.
type InvoicePDFRequester interface {
	RequestInvoicePDF(ctx context.Context, providerInvoiceID string) error
}

// CatalogProvider is an optional operator surface for inspecting and
// revalidating the active provider's configured product catalog. The API
// endpoint retains its historical billing-paddle-catalog path for client
// compatibility, but the response and this interface are provider-neutral.
// Providers whose catalog is dashboard-owned may return ErrNotImplemented for
// ResetBillingCatalog rather than pretending that a local reset deletes the
// remote products.
type CatalogProvider interface {
	ListBillingCatalog(ctx context.Context) []api.BillingCatalogEntry
	SyncBillingCatalog(ctx context.Context) ([]api.BillingCatalogEntry, error)
	ResetBillingCatalog(ctx context.Context) error
}

// EventType is the provider-neutral "what happened" classifier apid
// dispatches on. Mapping from the provider's payload lives inside each
// implementation's VerifyWebhook.
type EventType int

const (
	// EventUnknown is the zero value; VerifyWebhook returns it when the
	// provider-specific event has no mapping (the apid handler treats
	// it as a no-op 200 — Stripe expects 2xx for everything it didn't
	// recognize so it doesn't retry forever).
	EventUnknown EventType = iota

	// EventSubscriptionCreated is fired when a customer completes
	// first-time checkout. apid uses it to stamp the customer's
	// stripe_subscription_item on the account row.
	EventSubscriptionCreated

	// EventSubscriptionUpdated is fired on plan changes mid-cycle.
	// apid syncs accounts.plan from the provider's payload.
	EventSubscriptionUpdated

	// EventSubscriptionCanceled is fired when the customer or the
	// provider cancels the subscription. apid flips the account to
	// suspended.
	EventSubscriptionCanceled

	// EventSubscriptionPastDue is fired when the provider marks the
	// subscription past-due (mid-cycle failure, after grace). apid
	// flips the account to past_due.
	EventSubscriptionPastDue

	// EventPaymentSucceeded is fired when an invoice settles. On a
	// past_due → active flip, apid sends the recovery email.
	EventPaymentSucceeded

	// EventPaymentFailed is fired when a charge bounces. apid flips
	// the account active → past_due and sends the entry-point email.
	EventPaymentFailed

	// EventRefundProcessed is fired when a refund is issued against a
	// charge (Stripe: charge.refunded). apid emits a `refund.processed`
	// audit event with the operator's account ID and the refund
	// amount. The webhook is observational — the operator-initiated
	// refund path goes through Provider.Refund (below), not through
	// this event.
	EventRefundProcessed
)

// Name returns the canonical English label apid's log lines + audit
// ledger use. The strings are stable — the cmd/apid events audit-log
// metric (events_audit_log_emission) and the dunning timer key off
// these names, not the integer values.
func (t EventType) Name() string {
	switch t {
	case EventSubscriptionCreated:
		return "subscription_created"
	case EventSubscriptionUpdated:
		return "subscription_updated"
	case EventSubscriptionCanceled:
		return "subscription_canceled"
	case EventSubscriptionPastDue:
		return "subscription_past_due"
	case EventPaymentSucceeded:
		return "payment_succeeded"
	case EventPaymentFailed:
		return "payment_failed"
	case EventRefundProcessed:
		return "refund_processed"
	default:
		return "unknown"
	}
}

// InvoiceData is the provider-neutral invoice projection carried by webhook
// events. It is deliberately small and contains only fields the customer
// invoice history API can persist.
type InvoiceData struct {
	ProviderInvoiceID string
	Number            string
	Status            string
	PeriodStart       time.Time
	PeriodEnd         time.Time
	SubtotalCents     int64
	TaxCents          int64
	TotalCents        int64
	AmountPaidCents   int64
	Currency          string
	PDFAvailable      bool
}

// Event is the normalized envelope apid's dunning state machine
// dispatches on. Provider-shaped JSON stays inside each
// implementation; Raw carries the original body for debugging.
type Event struct {
	// EventID is the provider's delivery UUID — Stripe event.id
	// (evt_…) or Paddle event_id. Used by apid's webhook replay
	// dedupe (issue #294): the same EventID arriving twice inside
	// the 5-minute TTL is rejected with 200 + a webhook.replay_rejected
	// audit row. Empty when the upstream provider did not populate
	// the field; apid treats an empty EventID as "no dedupe" and
	// forwards the event (pre-#294 behaviour).
	//
	// GitHub webhooks carry the same UUID via the X-GitHub-Delivery
	// header, which gatewayd-internal consults directly without round-tripping
	// through this struct.
	EventID string

	// Type drives the apid switch statement. Unknown / unmapped
	// types render as a 200 no-op.
	Type EventType

	// CustomerID is the provider's customer handle (Stripe: cus_…,
	// Paddle: ctm_…). apid resolves this to a state.Account via
	// Store.AccountByProviderCustomerID.
	CustomerID string

	// PlanID is the provider's plan identifier (Stripe: plan_… /
	// price_…, Paddle: pri_…). apid maps it to api.Plan via
	// PlanFromProviderID; empty when the event carries no plan
	// change (payment events typically don't).
	PlanID string

	// SubscriptionID is the provider's subscription handle (Stripe:
	// sub_…, Paddle: sub_…). apid may stamp this on the account
	// row if empty.
	SubscriptionID string

	// Raw is the original webhook body, preserved for the audit log
	// and for downstream debugging. Provider-shaped JSON.
	Raw []byte

	// AmountCents is the integer-cents amount carried by the event
	// (refund events: the refund amount; payment events: the
	// amount_paid; subscription events: 0). Providers populate
	// during VerifyWebhook so the apid handler can stamp the
	// audit-log payload without re-parsing Raw.
	AmountCents int64

	// Currency is the provider's three-letter currency code (Stripe:
	// string(r.Currency)). Empty when the event carries no monetary
	// value or the provider did not populate it.
	Currency string

	// ProviderRefundID is the provider's refund handle (Stripe: re_…).
	// Only populated for refund events. apid logs it on the
	// `refund.processed` audit row.
	ProviderRefundID string

	// ChargeID is the provider's charge handle (Stripe: ch_…; Paddle:
	// tx_…). Only populated for refund events. apid logs it so an
	// operator can correlate the audit row with the provider
	// dashboard.
	ChargeID string

	// Invoice is populated by providers that deliver an invoice/order
	// projection (currently Paddle and Polar). It is persisted independently of the
	// event type so order.created can populate invoice history before payment.
	Invoice *InvoiceData
}

// RefundResult is what Provider.Refund returns on a successful refund.
// The handler stamps the fields onto the audit row and echoes the
// ProviderRefundID to the CLI so an operator can pull up the refund
// in the provider dashboard by ID.
type RefundResult struct {
	ProviderRefundID string
	ChargeID         string
	AmountCents      int64
	Currency         string
	// Status is the provider's current refund state. Providers may return a
	// pending state when a refund requires asynchronous approval.
	Status string
}

// ErrBadSignature is the unified error returned by VerifyWebhook when
// the signature header is malformed, missing, the timestamp is out of
// tolerance, or the HMAC does not match. Provider implementations
// must wrap with %w so callers can use errors.Is.
var ErrBadSignature = errors.New("billing: bad webhook signature")

// ErrNotImplemented is the unified error a Provider returns when the
// selected billing backend does not support a method. Callers should map this to a 501 Problem with
// docs_url pointing at the spec — the operator picks a backend that
// supports the surface they need.
var ErrNotImplemented = errors.New("billing: provider does not implement this method")

// ErrNoAPIKey is the sentinel the Stripe Client's requireAPI() helper
// returns when its SDK *client.API is nil — the operator set
// FAAS_STRIPE_API_KEY to empty (or set only the legacy webhook secret).
// apid's billing routes map this to a 502 Problem with a clear
// "operator mis-configuration" hint; the CLI's `faas billing status`
// prints it so operators can self-diagnose without paging support.
// Distinct from ErrNotImplemented (which means the provider
// intentionally lacks a surface) and from ErrBadSignature (which is
// webhook-only). Stripe-side only today; Paddle has no equivalent
// (its config loads a sandbox key or refuses to construct the Client).
var ErrNoAPIKey = errors.New("billing: provider API key not configured")

// ErrNoOpenCharge is returned by Provider.RetryLatestCharge when the
// account has no open invoice / transaction to retry. apid's retry
// handler maps this to 404 + a Problem whose detail explains the
// customer is already in good standing (the dunning email is
// stale). Distinct from a provider-side failure (502).
var ErrNoOpenCharge = errors.New("billing: no open charge to retry")

// ErrAlreadyCancelled is returned by Provider.CancelAtPeriodEnd when
// the account has no active subscription (Free plan, post-cancel, or
// never-checked-out). apid's cancel handler maps this to 409 +
// `code: already_cancelled` so the CLI can render a friendly "your
// subscription is already cancelled" hint instead of crashing. Stripe:
// returned only when the account has no StripeSubscriptionItem
// (Stripe's re-cancel of an already-cancelled sub is idempotent and
// returns 200). Paddle: returned when the customer has no scheduled
// recurring transaction; the SDK call itself distinguishes the
// "cancelled" vs "never subscribed" cases from the response shape.
var ErrAlreadyCancelled = errors.New("billing: subscription already cancelled or not active")

// PaymentMethod is the provider-agnostic card-on-file summary
// (issue #242). Lives in pkg/billing (NOT pkg/api) to avoid the
// `pkg/api ↔ pkg/state` cycle flagged in memory
// pkg-api-cannot-import-pkg-state.md — the apid handler converts to
// the wire DTO (api.PaymentMethodSummary) at the handler boundary.
// The zero value is the "no card on file" sentinel.
type PaymentMethod struct {
	Brand    string // lowercase network label: "visa", "mastercard", "amex"
	Last4    string // last 4 digits of the PAN — never the full PAN
	ExpMonth int    // 1-12
	ExpYear  int    // 4-digit year
}

// Classifier is the optional seam a Provider can implement to declare
// its push-error classification. meterd's pusher loop dispatches via
// this interface first so SDK-typed classification stays in the
// provider's own package (which knows about *stripe.Error /
// *paddleerr.Error) without forcing the billing.Provider interface
// wider. Returning "other" for an unknown inner error is the
// provider's contract; nil always returns "ok".
//
// Providers that don't implement this interface get meterd's default
// fallback ("other") — same as the prior all-Stripe dispatch. The
// pusher's opLabel/observer dispatch falls back to a Stripe-shaped
// histogram so a missing Classifier doesn't lose observations.
//
// Keep the label set closed per provider: pkg/billing/stripe's
// stripe.PushResultLabels and pkg/billing/paddle's paddle.PushResultLabels
// are each pre-instantiated in pkg/wire/metrics.go at registry
// construction time.
type Classifier interface {
	ClassifyPushError(err error) string
}

// Capability is a single bit in the CapabilitySet returned by
// Provider.Capabilities. Use CapabilitySet.Has to test for a given
// capability. Bits are stable and additive — new capabilities get
// new high bits; existing bits are never reassigned. The zero value
// is "no optional surfaces", which matches the bare Provider
// interface (a hypothetical minimum-viable provider that only
// implements the four primitives M7 needs).
type Capability uint64

const (
	// CapHostedCheckout means CreateUpgradeTransaction returns a
	// real transaction id + checkout URL. apid renders the URL on
	// the 402 Problem and the dashboard renders a hosted-checkout
	// button. Paddle: yes. Stripe: no (Stripe returns the
	// FAAS_BILLING_PORTAL_URL template instead, which is
	// operator-configured, not provider-generated).
	CapHostedCheckout Capability = 1 << iota

	// CapRefund means Provider.Refund is implemented. apid's
	// admin/refund route maps to a 502 when absent. Paddle and
	// Stripe both expose this surface.
	CapRefund

	// CapUsageReconcile means Provider.ReconcileUsage is implemented
	// and returns real pushed-mb_seconds. The reconciler
	// (pkg/billing/reconciler) skips accounts whose active provider
	// lacks this capability, before any SDK call fires. Paddle: no
	// (Paddle Billing has no usage-summary endpoint). Stripe: yes.
	// Polar: yes when a meter ID is configured.
	CapUsageReconcile

	// CapSandbox means the provider supports a sandbox / test
	// environment toggle (Paddle: FAAS_PADDLE_SANDBOX, Stripe: sk_test_*
	// API key prefix). The provider's config struct exposes it;
	// Provider.Capabilities() surfaces it so admin/CLI can report
	// it without re-reading the config.
	CapSandbox

	// CapUsageMetered means PushUsageRecord posts a metered
	// UsageRecord against the customer's subscription item
	// (Stripe's shape). Paddle: no (Paddle posts a flat-rate
	// line item per push). Stripe: yes.
	CapUsageMetered

	// CapUsageLineItem means PushUsageRecord posts a one-shot
	// line item per call (Paddle's shape). Stripe: no. apid /
	// meterd do not dispatch on this — the provider's own
	// implementation handles the wire shape — but it is exposed
	// for the admin/CLI surface so operators can see the
	// push-model a provider uses.
	CapUsageLineItem
)

// CapabilitySet is the bitmask of capabilities a Provider exposes.
// Construct via OR of the Cap* constants. The zero value is the bare
// interface (no optional surfaces).
type CapabilitySet uint64

// Has reports whether the set contains the given capability. Callers
// should use this in preference to errors.Is(err, ErrNotImplemented)
// when the dispatch table can be built once at boot.
func (s CapabilitySet) Has(c Capability) bool { return s&CapabilitySet(c) != 0 }

// String returns the canonical comma-separated list of named
// capabilities (e.g. "hosted_checkout,usage_line_item,sandbox").
// Used by GET /v1/admin/billing-provider and the CLI's
// `faas billing status` output. The order matches the iota declaration
// order so the output is stable across runs.
func (s CapabilitySet) String() string {
	if s == 0 {
		return "none"
	}
	parts := make([]string, 0, 6)
	for _, c := range []struct {
		cap  Capability
		name string
	}{
		{CapHostedCheckout, "hosted_checkout"},
		{CapRefund, "refund"},
		{CapUsageReconcile, "usage_reconcile"},
		{CapSandbox, "sandbox"},
		{CapUsageMetered, "usage_metered"},
		{CapUsageLineItem, "usage_line_item"},
	} {
		if s.Has(c.cap) {
			parts = append(parts, c.name)
		}
	}
	return strings.Join(parts, ",")
}
