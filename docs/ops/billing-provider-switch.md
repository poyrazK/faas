# Billing provider switch (Stripe ↔ Paddle)

The platform can run on either Stripe (default) or Paddle Billing v2
(opt-in) via a single env-var selector (`FAAS_BILLING_PROVIDER`). This
runbook covers switching a deployment between the two without code
changes, migrations, or per-handler branching.

The decision that a deployment uses Paddle at all is operator-side —
`ex44_faas_financial_model.xlsx` keeps Stripe as the production
reference for fee math; Paddle is the secondary surface for customers
whose card issuers don't process USD-denominated Stripe charges.

## Selector

| `FAAS_BILLING_PROVIDER` | Behavior                                         |
|-------------------------|--------------------------------------------------|
| (empty) / unset         | Stripe (default — bit-for-bit unchanged).        |
| `stripe`                | Stripe (explicit).                               |
| `paddle`                | Paddle Billing v2. Requires `FAAS_PADDLE_*`.    |
| anything else           | Daemon fails to boot with a typed error.         |

The selector is the canonical name for both `apid` and `meterd`; both
daemons import the same loader (`pkg/billing/loader`) so a config drift
between them cannot happen.

## Env vars

### Stripe (default — pre-existing)

| Var                       | Required         | Used by                  |
|---------------------------|------------------|--------------------------|
| `STRIPE_API_KEY`          | Yes (paid plans) | apid + meterd            |
| `STRIPE_WEBHOOK_SECRET`   | Yes              | apid (`/v1/webhooks/stripe`) |
| `FAAS_BILLING_PORTAL_URL` | Recommended      | apid (changePlan 402 template) |

### Paddle (opt-in)

| Var                          | Required | Used by                                       |
|------------------------------|----------|-----------------------------------------------|
| `FAAS_PADDLE_API_KEY`        | Yes      | apid + meterd                                 |
| `FAAS_PADDLE_WEBHOOK_SECRET` | Yes      | apid (`/v1/webhooks/paddle`)                  |
| `FAAS_PADDLE_SANDBOX`        | Recommended for non-prod (`1` / `true`) | apid + meterd — sandbox host (`api.sandbox.paddle.com`) vs production (`api.paddle.com`) |

The Paddle webhook secret is the shared HMAC-SHA256 key Paddle's
dashboard shows under **Developer tools → Notifications → Endpoints**.
The same key signs all events for that endpoint; the
`Paddle-Signature` header carries `ts=…;h1=…` and
`paddle.Provider.VerifyWebhook` checks both halves with a 5-minute
clock-skew tolerance.

## Cutover procedure (Stripe → Paddle)

1. **Inventory the existing customer mappings.** Stripe
   `cus_…` values live in `accounts.stripe_customer_id`. Paddle uses
   `ctm_…`. The column is **reused** for both providers per ADR-032
   (the rename to `provider_customer_id` is a separate follow-up PR).
   New Paddle customers get fresh `ctm_…` IDs on first checkout; there
   is no in-place cus→ctm migration.

2. **Provision the Paddle credentials.**
   - Generate a Paddle API key (sandbox or live).
   - Create a webhook endpoint in the Paddle dashboard pointing at
     `https://apid.DOMAIN/v1/webhooks/paddle`. Copy the webhook secret.

3. **Stand up the new billing surface in parallel.** The two providers
   can coexist in different environments — do not point the production
   apid at Paddle until a small set of test customers has completed
   first-time checkout + a paid upgrade + a payment-failed recovery
   on the sandbox.

4. **Set the env vars on apid + meterd.** Restart both daemons. Boot
   logs include the line `billing provider loaded provider=paddle` —
   absence of this line means the env var didn't reach the daemon
   (systemd drop-in mis-config, container env filter, etc.).

5. **Watch the dunning state machine for one full cycle.** A redelivered
   Paddle event should land in `journalctl -u faas-apid` as a 200
   with no side effect; a `transaction.payment_failed` flips the
   seeded account to `past_due`; a `transaction.paid` flips it back
   to `active`.

## Cutover procedure (Paddle → Stripe)

Unset `FAAS_BILLING_PROVIDER` and redeploy. Stripe is the default, so
the absence of the var is sufficient. The Stripe path is bit-for-bit
unchanged from pre-PR-#3 — the apid changePlan 402 returns
`billing_portal_url` with the `{account_id}` substitution, the
`/v1/webhooks/paddle` mount returns 503 if a request does land there
(provider not configured), and meterd pusher loop dispatches through
the legacy `stripe.Client`.

## Failure modes

| Symptom                                                    | Likely cause                                                                  |
|------------------------------------------------------------|-------------------------------------------------------------------------------|
| Boot fails with `unknown FAAS_BILLING_PROVIDER`            | Typo in the env var (e.g. `braintree`, `paypal`). Set to `paddle` or unset. |
| Boot fails with `paddle EnsurePlanProducts: …`             | Network egress to `api.paddle.com` blocked (or `api.sandbox.paddle.com` if sandbox=1). Check `iptables` + `FAAS_BRIDGE_OUTBOUND`. |
| Webhook returns 503                                        | `FAAS_PADDLE_WEBHOOK_SECRET` is empty. Provider refuses to verify.            |
| Webhook returns 400 (`code: validation_failed`)            | Signature mismatch (wrong secret in dashboard) or clock skew > 5 min.         |
| `transaction.paid` 200 but no state flip                   | Unknown customer (event's `data.customer_id` doesn't match `accounts.stripe_customer_id`). 200 stops Paddle from retrying; check the customer mapping. |
| `changePlan` 402 carries `paddle_checkout_url` but URL 404 | Paddle sandbox product/price IDs not yet created. Run `EnsurePlanProducts` manually (it's idempotent — re-running on an existing catalog is a no-op). |

## Secret rotation

Paddle API keys and webhook secrets live at
`/etc/faas/secrets/paddle.{api_key,webhook_secret}` (mode `0440`,
owner `root:faas`), sealed at rest per gap G2 (spec §17). Rotation
procedure:

1. Generate the new key/secret in the Paddle dashboard.
2. Install on the EX44 with `install -m 0440 -o root -g faas`.
3. `systemctl restart faas-apid faas-meterd` — both daemons read the
   env vars at boot; mid-flight rotation requires a process restart.

## Health checks

- `/v1/webhooks/paddle` should respond 503 (provider not configured) on
  a Stripe-default box — a 200 here is a bug.
- The `apid` boot log line `billing provider loaded provider=paddle`
  is the canonical "the env var reached the daemon" signal.
- `meterd` boot log: `meterd billing provider loaded provider=paddle`.

## Related

- ADR-032 — Paddle as an opt-in billing provider (decision record).
- ADR-025 — provider-pluggable billing layer (the abstraction).
- `pkg/billing/loader/` — the canonical selector implementation.
- `pkg/billing/paddle/` — the Paddle Billing v2 implementation.