# ADR-032 · Paddle as an opt-in billing provider (post-Stripe)

- **Status:** accepted
- **Date:** 2026-07-24
- **Supersedes:** none
- **Related:** ADR-025 (provider-pluggable billing layer), §14 M7 acceptance,
  ex44_faas_financial_model.xlsx (operator standing decision — Stripe
  pricing remains the production reference).

## Context

ADR-025 added `pkg/billing.Provider` and PR #1+#2 extracted the Stripe
facade and added a Paddle Billing v2 implementation. PR #3 (this ADR's
landing PR) wires the platform end-to-end on either Stripe (default) or
Paddle (opt-in via `FAAS_BILLING_PROVIDER=paddle`) with a single env-var
selector, no per-handler branching, and bit-for-bit identical dunning
state machine + customer-facing email flows.

The operator's standing decision (per `ex44_faas_financial_model.xlsx`
review) is to keep Stripe as the production default. Paddle is a
secondary surface for customers whose card issuers won't process
USD-denominated Stripe charges (the operator's home country is one of
those).

## Decision

1. **`FAAS_BILLING_PROVIDER`** selects the provider at daemon boot.
   Empty / unset / `"stripe"` → Stripe (default, bit-for-bit unchanged
   from pre-PR-#3). `"paddle"` → Paddle Billing v2 (sandbox or
   production gated by `FAAS_PADDLE_SANDBOX=1`). Any other value →
   the daemon fails to boot with a typed error.

2. **`pkg/billing.Provider` gains a 5th method,
   `CreateUpgradeTransaction(ctx, acct, plan) (txID, checkoutURL, err)`.**
   Stripe returns `("", "", nil)` — the apid handler reads `txID == ""`
   to fall back to the precomputed `FAAS_BILLING_PORTAL_URL` template.
   Paddle implements it for real via `paddle.Client.CreateTransaction`
   against the per-plan monthly price; returns
   `(txn_…, https://paddle.checkout/…, nil)`. The two paths are
   mutually exclusive on a single 402 — exactly one of
   `billing_portal_url` or `paddle_checkout_url`+`tx_id` is set on the
   RFC 7807 Problem extensions.

3. **`accounts.stripe_customer_id` is reused for the Paddle
   `ctm_…` customer id.** A column rename
   (`stripe_customer_id` → `provider_customer_id`) is a separate, smaller
   migration PR — out of scope here to keep this PR reviewable in
   ~10 minutes. The state.Store mirror methods
   `AccountByPaddleCustomerID` + `UpdateAccountPaddleCustomerID` are
   1-line pass-throughs to the existing Stripe methods; their existence
   is documentation, not a different column.

4. **No HTTP-transport `Idempotency-Key` injection for Paddle today.**
   `paddle-go-sdk/v5@v5.2.0` has no request option for the header;
   idempotency is recorded in `CustomData["faas_paddle_idem_key"]`. A
   transport-level wrapper is the durable fix and lands in a follow-up
   PR (the dedupe behavior is correct under CustomData tagging; this
   is a hardening, not a correctness, gap).

5. **The dunning state machine is provider-neutral.** Both webhooks
   (Stripe + Paddle) dispatch into a single `handleBillingEvent(ctx,
   ev, acct)` helper that emits the same status transitions (active /
   past_due / suspended / plan-change) and the same email surface
   (PR #133's `mail.PaymentFailedBody` + `mail.AccountRestoredBody`).
   The four M7 dunning transitions are unchanged on the wire.

## Consequences

### Positive

- Operators can move a deployment to Paddle by setting two env vars
  (`FAAS_BILLING_PROVIDER=paddle` + `FAAS_PADDLE_API_KEY` +
  `FAAS_PADDLE_WEBHOOK_SECRET` + `FAAS_PADDLE_SANDBOX=1` for the
  sandbox) — no code changes, no migrations, no provider-shaped
  branching in the daemons.
- The dunning state machine stays in one place (`handleBillingEvent`)
  so a future third provider (Braintree, Lemon Squeezy, etc.) only
  needs to implement the 5-method Provider interface — same shape as
  PR #1's Stripe extraction, validated by the two compile-time
  conformance assertions
  (`var _ billing.Provider = (*stripe.Client)(nil)` and
  `var _ billing.Provider = (*paddle.Provider)(nil)`).

### Negative / deferred

- The column-rename is a follow-up. Until then, the
  `accounts.stripe_customer_id` column carries the Paddle `ctm_…` value,
  and any grep against the column name produces false positives. The
  dedicated `AccountByPaddleCustomerID` / `UpdateAccountPaddleCustomerID`
  methods keep the call sites self-documenting.
- The Stripe stub returns `("", "", nil)` for `CreateUpgradeTransaction`
  — a deliberate empty-string dispatch signal. The apid handler reads
  `txID == ""` to branch on the template path. A future Provider that
  also uses a template URL must match this contract (or the dispatch
  in `handlers_ext.go::changePlan` grows a per-provider type assertion,
  which ADR-025 explicitly forbids).
- The Paddle error classifier (a stable label per failure mode,
  matching `stripe.ClassifyPushError`) is not shipped today. Paddle
  push errors collapse to "other" in the meterd `_ops_total` label set.
  A separate slice follow-up lands the classifier.

### Rollback

Unset `FAAS_BILLING_PROVIDER` (Stripe default returns) and redeploy.
The Provider interface additions are additive; the existing Stripe
code path is unchanged. Paddle webhook entries (`/v1/webhooks/paddle`)
fall through to a 503 with no provider configured, so a partially
rolled-back deployment does not process Paddle events without
verification.

## PR split (re-record from PR #3 plan)

- PR #1 — extracted `pkg/stripex` → `pkg/billing/stripe`, defined
  `pkg/billing.Provider`, added the `Event` + `EventType` envelope.
- PR #2 — added `pkg/billing/paddle` (HMAC-SHA256 verify, calendar-
  month overage, 11 unit tests + sandbox tests).
- PR #3 (this ADR's landing PR) — rewires apid + meterd to dispatch
  through the same Provider interface, adds the 5th method
  (`CreateUpgradeTransaction`), and ships the operator runbook.
- PR #4 (deferred) — dashboard + CLI surface for `paddle_checkout_url`
  rendering; column rename to `provider_customer_id`.