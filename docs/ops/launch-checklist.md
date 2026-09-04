# Launch checklist — Polar-default at v1.0

This is the operator gate for the v1.0 release. Polar is the public-release
merchant of record and the implicit billing-provider default. The checklist
must be completed against the same Polar environment and catalog used by the
production deployment.

## Pre-launch verification gates

### 1. Automated billing and metering suite

- **Run:** `go test ./pkg/billing/... ./pkg/meter ./pkg/billing/reconciler ./cmd/apid`.
- **Pass criterion:** all tests pass, including Polar catalog, webhook,
  deferred plan-change, durable usage replay, cap-boundary, and reconciliation
  failure tests.
- **Signed:** ____________________  date: _________

### 2. Polar sandbox smoke walk

- **Prepare:** create the Hobby, Pro, and Scale monthly products in the Polar
  sandbox; attach the fixed monthly price and the `faas_ram_usage` metered
  price; configure the meter to sum `gb_ram_hours`; register
  `https://<host>/v1/webhooks/polar` with the Standard Webhooks secret.
- **Exercise:** complete a fresh checkout, confirm `subscription.created` and
  `order.paid` update the account, push one completed usage window, schedule a
  paid-to-paid downgrade, and verify the provider portal opens. Also exercise
  a failed payment and recovery delivery.
- **Pass criterion:** the account is bound to the Polar customer and
  subscription IDs, usage is visible in the Polar meter, the local plan stays
  unchanged until the subscription update webhook, and invoice history is
  populated without webhook retries caused by slow PDF generation.
- **Evidence:** attach the provider event IDs and the corresponding apid and
  meterd log excerpts to the release record.
- **Signed:** ____________________  date: _________

### 3. Deployment configuration and boot gate

- **Run:** `make verify-secrets` after rendering `/etc/faas/sealed.env` and
  the daemon TOML files.
- **Verify:** `FAAS_BILLING_PROVIDER` is unset or `polar`; the Polar access
  token, webhook secret, three product IDs, usage event name, and meter ID are
  present and refer to the same environment.
- **Pass criterion:** both daemons boot with
  `billing provider loaded provider=polar` and
  `meterd billing provider loaded provider=polar`; neither daemon logs a
  catalog-preflight failure.
- **Signed:** ____________________  date: _________

### 4. Observability and recovery gate

- **Verify:** `/v1/webhooks/polar` rejects bad signatures, valid deliveries
  are acknowledged after durable processing, and `meterd` exposes
  `meterd_billing_drift_reconcile_failures_total` alongside the drift gauges.
- **Exercise:** temporarily make the Polar API unavailable, confirm the
  meterd health/log surface records a failed push, restore access, and confirm
  the hourly durable lookback replays the pending usage exactly once.
- **Pass criterion:** no usage window is silently discarded, provider failures
  are visible to operators, and the failure counter returns to a zero rate
  after recovery.
- **Signed:** ____________________  date: _________

## Legacy-provider compatibility

Paddle remains an explicit compatibility option for existing deployments and
is covered by `make e2e-sandbox`. Run that walk only when changing the Paddle
adapter; it is not the Polar public-release acceptance gate. Stripe remains an
explicit legacy opt-in through `FAAS_BILLING_PROVIDER=stripe`.

## Post-launch rollback

1. Set `FAAS_BILLING_PROVIDER=paddle` with complete Paddle credentials for a
   Paddle fallback, or `FAAS_BILLING_PROVIDER=stripe` with the legacy Stripe
   credentials for a Stripe fallback.
2. Restart `faas-apid` and `faas-meterd`.
3. Confirm the corresponding provider name in both boot logs and monitor the
   provider-specific webhook and usage metrics.
4. Do not assume Polar customer or subscription IDs can be reused by another
   provider; provider mappings are not portable.

## Final sign-off

All four gates green. The maintainer signs the v1.0.0 tag push.

- **Maintainer:** ____________________
- **Date:** ____________________
- **`v1.0.0` tag:** pushed at ____________________
- **Release flow:** see `.github/workflows/release.yml`.
