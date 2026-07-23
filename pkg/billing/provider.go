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
//     Default provider when FAAS_BILLING_PROVIDER is empty.
//   - pkg/billing/paddle — Paddle Billing v2 (current API). Opt-in via
//     FAAS_BILLING_PROVIDER=paddle.
//
// Provider-specific behaviour stays inside each implementation; the rest
// of the codebase (apid, meterd, the dunning state machine, the email
// surface) talks only to the Provider interface.
package billing

import (
	"context"
	"errors"
	"time"

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
type Provider interface {
	// EnsurePlanProducts is the idempotent product/price setup at boot.
	// Stripe: stripe.Plans.List + stripe.Plans.New by Nickname. Paddle:
	// paddle.Items.List + paddle.Items.Create by description match.
	// Idempotent across restarts so a redelivered boot is a no-op.
	EnsurePlanProducts(ctx context.Context) error

	// CreateCustomer maps a state.Account to the provider's customer
	// handle and writes the ID back via
	// state.Store.UpdateAccountStripeCustomerID. The column name is
	// intentionally stale (the stripe-only era) — a column rename is
	// a separate, smaller migration PR and out of scope for ADR-025.
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
	// idempotency-key every external call against (accountID, hour-or-month).
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
	default:
		return "unknown"
	}
}

// Event is the normalized envelope apid's dunning state machine
// dispatches on. Provider-shaped JSON stays inside each
// implementation; Raw carries the original body for debugging.
type Event struct {
	// Type drives the apid switch statement. Unknown / unmapped
	// types render as a 200 no-op.
	Type EventType

	// CustomerID is the provider's customer handle (Stripe: cus_…,
	// Paddle: ctm_…). apid resolves this to a state.Account via
	// Store.AccountByStripeCustomerID.
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
}

// ErrBadSignature is the unified error returned by VerifyWebhook when
// the signature header is malformed, missing, the timestamp is out of
// tolerance, or the HMAC does not match. Provider implementations
// must wrap with %w so callers can use errors.Is.
var ErrBadSignature = errors.New("billing: bad webhook signature")
