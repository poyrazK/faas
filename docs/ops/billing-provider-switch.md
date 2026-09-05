# Billing provider operations

Polar is Gregale’s public-release merchant of record and the default provider.
Paddle and Stripe remain explicit compatibility options. `apid` and `meterd`
use the same selector and fail closed when the selected provider is not
configured.

## Selector

| `FAAS_BILLING_PROVIDER` | Behavior |
|---|---|
| empty / unset | Polar (public-release default) |
| `polar` | Polar MoR; requires access token, webhook secret, three products, and meter |
| `paddle` | Paddle compatibility provider; requires `FAAS_PADDLE_*` |
| `stripe` | Stripe legacy provider; requires the legacy `STRIPE_*` path |
| anything else | Daemon boot fails with an unknown-provider error |

The selector is the canonical name for both daemons. Environment values
override the matching `[billing.*]` TOML block.

## Polar configuration

Required on `apid` and `meterd`:

| Variable | Purpose |
|---|---|
| `FAAS_POLAR_ACCESS_TOKEN` | Polar organization access token |
| `FAAS_POLAR_HOBBY_PRODUCT_ID` | Monthly Hobby product UUID |
| `FAAS_POLAR_PRO_PRODUCT_ID` | Monthly Pro product UUID |
| `FAAS_POLAR_SCALE_PRODUCT_ID` | Monthly Scale product UUID |
| `FAAS_POLAR_METER_ID` | Usage meter UUID |

Required on `apid`:

| Variable | Purpose |
|---|---|
| `FAAS_POLAR_WEBHOOK_SECRET` | Standard Webhooks signing secret |

Optional variables are `FAAS_POLAR_SANDBOX`, `FAAS_POLAR_USAGE_EVENT_NAME`
(default `faas_ram_usage`), `FAAS_POLAR_SUCCESS_URL`,
`FAAS_POLAR_RETURN_URL`, `FAAS_POLAR_BASE_URL`, and
`FAAS_POLAR_WEBHOOK_TOLERANCE_SECONDS` (default 300 seconds).

Create one active monthly recurring product per paid plan in the selected
Polar environment. Each product must contain the fixed monthly price and a
metered EUR price backed by the configured meter. The meter must sum the
`gb_ram_hours` property for the configured event name. Gregale removes the
included calendar-month allowance locally before sending overage events, so
do not add a Polar meter-credit benefit to these products.

`apid` validates the catalog at boot. `meterd` validates it before starting
the usage loop. A missing product, archived resource, wrong recurring period,
wrong fixed price, wrong meter, or meter-credit benefit prevents startup.

## Runtime behavior

- Polar checkout creates/reuses a customer using Gregale’s external account ID.
- Customer portal sessions are created on demand for payment-method and
  subscription management.
- Plan upgrades use hosted checkout when no subscription exists. Three
  surfaces hand the customer to that checkout and share one apid routine
  (`beginHostedCheckout`): `PATCH /v1/account/plan` answers 402 with
  `checkout_url`; `gregale plan <plan>` prints and opens that URL in text
  mode (exit 0 — the plan flips on the webhook) and emits the RFC 7807
  problem under `--json`; the dashboard billing page links Free accounts
  to `/dashboard/upgrade?plan=…`, a one-form confirmation page whose POST
  redirects to the provider checkout. The local plan changes only when the
  provider webhook confirms payment.
- Paid-to-paid downgrades are scheduled with the provider for the next period;
  the local entitlement remains active until a subscription webhook confirms
  the change. Free is represented by cancellation at period end.
- When the provider then ends the subscription (`subscription.revoked`, or
  `subscription.canceled` with a non-active status; Paddle
  `subscription.canceled`; Stripe `customer.subscription.deleted`), apid
  clears the subscription binding and sets the plan to Free (spec §4.7). An
  active account stays active and receives one "subscription ended" email; a
  `past_due` or `suspended` account keeps its dunning stamp so the
  non-payment ladder continues. A revoke for a subscription that is no
  longer the account's current one is ignored. The customer can upgrade
  again through hosted checkout immediately.
- `meterd` pushes completed UTC-hour net overage events and replays a durable
  30-day lookback after restart or provider outage. Usage is not marked
  complete until the provider call succeeds.
- Overage caps fail closed. A window that would cross the monthly cap is held
  for a later replay rather than partially billed under an hourly dedupe key.
- Polar invoice PDF generation is retried asynchronously after the webhook is
  acknowledged; invoice persistence and entitlement updates remain retryable
  on database failure.
- Polar does not expose a direct saved-card retry operation. `faas billing
  retry` returns an unsupported result with the portal fallback; the customer
  must update the payment method in the portal.
- Operator refunds use `POST /v1/admin/accounts/{id}/refunds`. The route
  requires the local invoice UUID, a positive EUR-cent amount, a reason, and
  an explicit `Idempotency-Key`; it binds the Polar order to the target
  account before calling Polar and requires the admin scope plus the operator
  email allowlist. The Polar provider's idempotency key is the recovery path
  when the response to a money-moving request is ambiguous.

## Operator checks

After rendering deployment configuration:

```sh
sudo deploy/scripts/verify-secrets.sh
```

Confirm both boot logs contain their provider name:

```text
billing provider loaded provider=polar
meterd billing provider loaded provider=polar
```

The admin catalog endpoint retains its historical path,
`/v1/admin/billing-paddle-catalog`, but is provider-neutral at runtime. It
returns Polar product IDs after the successful catalog preflight, so
`faas billing status` and `faas billing price-catalog sync` work with Polar as
well as Paddle. A provider without this operator surface returns 501.

Monitor these metrics and logs:

- `meterd_billing_drift_mb_seconds` and `meterd_billing_drift_ratio` show the
  last successful provider-vs-local comparison.
- `meterd_billing_drift_reconcile_failures_total{provider,reason}` shows when
  those gauges may be stale.
- `polar_webhook.verify_failed` and `polar_webhook.unknown_customer` identify
  signature or customer-binding failures.
- `meter` logs with `replay=true` show durable usage recovery after an outage.

## Switching providers

Provider switching is a deployment operation, not an account-data migration.
Set the selector explicitly, provide the complete credentials/catalog for the
new provider, and restart both daemons together. Do not reuse a Polar customer
or subscription ID in Paddle or Stripe; provider identifiers are not portable.

For a Paddle compatibility deployment:

```text
FAAS_BILLING_PROVIDER=paddle
FAAS_PADDLE_API_KEY=...
FAAS_PADDLE_WEBHOOK_SECRET=...
FAAS_PADDLE_SANDBOX=1       # sandbox only
```

For the legacy Stripe path:

```text
FAAS_BILLING_PROVIDER=stripe
STRIPE_API_KEY=...
STRIPE_WEBHOOK_SECRET=...
```

Restart with `systemctl restart faas-apid faas-meterd`, then verify the
provider name in both boot logs and use the provider-specific webhook URL.
Unset the selector only when Polar is fully configured, because empty now
means Polar.

## Secret rotation

Rotate the active provider credentials in the deployment secret store, render
the service configuration, and restart both billing daemons together. Verify
`faas billing status`, the provider name in both boot logs, and one signed
webhook or sandbox checkout before revoking the old credential. See
[`secrets-rotation.md`](secrets-rotation.md) for the cadence and host-secret
handling rules.

## Webhook and failure handling

Register `https://<host>/v1/webhooks/polar` and subscribe to subscription
creation/update/cancellation/past-due/revocation plus order creation/payment/
refund events. Polar Standard Webhooks headers are verified before parsing;
database persistence and replay-claim failures return non-2xx so the provider
can retry. A valid duplicate delivery is acknowledged without repeating the
state transition.

The hourly pusher is intentionally at-least-once. If Polar is unavailable,
the failed window remains durable and is retried on the next pass. Investigate
the provider failure counter and logs before clearing any local usage data.

## References

- [Polar checkout sessions](https://polar.sh/docs/api-reference/checkouts/create-session)
- [Polar customer portal sessions](https://polar.sh/docs/api-reference/customer-portal/sessions/create)
- [Polar event ingestion](https://polar.sh/docs/api-reference/events/ingest)
- [Polar meter quantities](https://polar.sh/docs/api-reference/meters/get-quantities)
- [Polar usage meters](https://polar.sh/docs/features/usage-based-billing/meters)
- [Polar subscription management](https://polar.sh/docs/features/subscriptions/manage)
- [Polar Standard Webhooks](https://polar.sh/docs/integrate/webhooks/delivery)
