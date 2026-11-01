# BillingDrift

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`,
the `BillingDrift` + `BillingDriftAccount` alert blocks
(ADR-049 §B.1).

Metrics: `meterd_billing_drift_mb_seconds{account_id, provider}`
(signed delivered − provider) and `meterd_billing_drift_ratio{account_id,
provider}` (abs(drift) / max(delivered, provider)) over the rolling
24 h window. "Delivered" is what meterd's pusher recorded as handed to
the provider (`billing_usage_deliveries` receipts), not usage
(ADR-20261031161842): usage above the spend cap, in hours not yet final,
or of a suspended subject is correctly undelivered and is not drift.
Whether the receipts match what should have been billed is the ledger
dual run's job (`meterd_financial_ledger_reconcile_accounts`). Company
organizations that own resources are billing subjects; their
`account_id` label carries the organization ID. Reconciliation failures are counted by
`meterd_billing_drift_reconcile_failures_total{provider,reason}`;
when non-zero, the gauges may be stale. Capability health is explicit in
`meterd_billing_reconcile_supported{provider}`; zero means drift is unknown,
not healthy. Emitted by
`pkg/billing/reconciler`, which runs on a 6 h meterd cron tick
(default `FAAS_RECONCILE_INTERVAL`).

Severity: `warn` (fleet-wide and per-account; ADR-1236 demoted the
fleet-wide page, since drift waits safely for business hours). The
fleet-wide alert fires on `> 0.5%` fleet-wide drift for `> 1h` — a real
Polar, Paddle, or Stripe outage has the potential to silently lose
invoices. The per-account `warn` alert catches single-customer
drift early so an operator can investigate before the customer
notices. These alerts carry `public_status="internal"`: they remain
operator-actionable but do not mark the customer request plane degraded.

## Symptom

The alert fires when the reconciler's drift ratio exceeds the
threshold over the rolling window:

| Alert | Trigger | For | Severity |
|---|---|---|---|
| `BillingDrift` | `max by (provider) (meterd_billing_drift_ratio) > 0.005` | 1h | page |
| `BillingDriftAccount` | `meterd_billing_drift_ratio > 0.05` | 15m | warn |
| `BillingReconcileFailures` | `rate(meterd_billing_drift_reconcile_failures_total[10m]) > 0` | 10m | page |
| `BillingReconciliationUnavailable` | `meterd_billing_reconcile_supported == 0` | 5m | page |

The `provider` label distinguishes Polar, Paddle, and Stripe. The
`account_id` label carries the offending customer id (omitted on
the fleet-wide rule, which aggregates by `provider`).

A positive drift (delivered > provider) means the provider lacks
usage events the pusher saw accepted; a negative one (provider >
delivered) means the provider counts events meterd has no receipt for,
such as a duplicate push or a push whose receipt write failed (the
next pass re-sends it under the same external ID and records the
receipt). Both are revenue or reputational risks.

## Verify

The reconciler is read-only against the provider surface. The
delivered-vs-provider comparison runs in `pkg/billing/reconciler` on
every `FAAS_RECONCILE_INTERVAL` tick (default 6 h). The Pusher
(`pkg/meter/pusher.go`) is the write-side complement — it runs on
the hourly cadence and durably replays completed windows after a
restart or provider outage.

> **A stale or missing gauge is not a zero.** If reconciliation fails,
> the account gauge is left unchanged and
> `meterd_billing_drift_reconcile_failures_total` increments. Treat
> the failure alert as authoritative until a successful reconcile
> refreshes the gauge; do not clear an incident because the last gauge
> happened to be below threshold.

For ad-hoc queries:

```bash
# Current per-account drift (the warn alert's source expression)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_billing_drift_ratio' | jq .

# Fleet-wide drift by provider (the page alert's source expression)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=max by (provider) (meterd_billing_drift_ratio)' | jq .

# Signed drift in mb_seconds — direction tells you which side is
# under-reported
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_billing_drift_mb_seconds' | jq .

# Per-account detail — replace $ACCOUNT_ID
ACCOUNT_ID=<uuid>
curl -fsS --data-urlencode "query=meterd_billing_drift_mb_seconds{account_id=\"${ACCOUNT_ID}\"}" \
  'http://127.0.0.1:9095/api/v1/query' | jq .

# Provider reconciliation failures (the page alert's source)
curl -fsS --data-urlencode \
  'query=sum by (provider, reason) (increase(meterd_billing_drift_reconcile_failures_total[1h]))' \
  'http://127.0.0.1:9095/api/v1/query' | jq .
```

## Check

```bash
# Confirm meterd is running the reconciler cron (look for the
# six-hour tick — recent journal shows the latest reconcile)
journalctl -u meterd --since '-7h' --no-pager | grep -i "reconcile\|drift"

# Recent meterd logs for any per-account reconcile failures
# (these are fail-soft warnings; the loop continues)
journalctl -u meterd --since '-7h' --no-pager | grep -i "reconcile account failed"

# Check for failed reconciles and classify the failure source
curl -fsS http://127.0.0.1:9090/metrics | grep meterd_billing_drift_reconcile_failures_total

# Confirm the reconciler is registered with the /metrics endpoint
curl -fsS http://127.0.0.1:9090/metrics | grep meterd_billing_drift

# Cross-check the delivered receipts for the offending subject over
# the reconciler's window (replace $ACCOUNT_ID). Polar buckets usage
# by the time meterd sent it, so its receipts are read by delivered_at;
# Stripe and Paddle bucket by usage hour (window_start).
ACCOUNT_ID=<uuid>
psql -t -A -c "select delivered_at, window_start, mb_seconds from billing_usage_deliveries where provider = 'polar' and account_id = '${ACCOUNT_ID}' and delivered_at >= date_trunc('hour', now()) - interval '24 hours' and delivered_at < date_trunc('hour', now()) order by delivered_at"
```

Compare the hours with the provider's hourly quantities for the same
customer to find the missing or doubled event.

A drift paired with low `meterd_billing_drift_mb_seconds` per-account
count indicates a transient provider blip (Polar/Paddle/Stripe 5xx,
rate-limit) — fail-soft handles these; the next tick re-queries.
A drift paired with the same `account_id` firing repeatedly across
multiple ticks means the provider is missing records for that
customer — escalate.

A fleet-wide `BillingDrift` on one provider but not the others means
that provider is the affected surface. A fleet-wide drift on all
providers is rare and indicates an upstream Postgres issue (the
receipts would be the side most likely wrong) — check meterd's DB
connectivity first.

## Silence

```bash
# Silence a known provider outage window — wait for the provider
# status page to recover before removing the silence.
amtool silence add \
	--matchers='alertname="BillingDrift",provider="stripe"' \
	--duration=2h \
	--comment='provider status page reports incident; tracking in the incident ticket'

# Silence per-account drift for a customer migrating plans
# (their billing surface is in flux; the drift is expected)
ACCOUNT_ID=<uuid>
amtool silence add \
  --matchers='alertname="BillingDriftAccount",account_id="'"${ACCOUNT_ID}"'"' \
  --duration=24h \
  --comment='customer mid-migration; provider summary endpoint lags'
```

## Recover

Three-step cascade, ordered from least to most disruptive:

1. **Identify the affected provider.** The fleet-wide rule's
   `provider` label tells you immediately. For Stripe: check
   [status.stripe.com](https://status.stripe.com) for an active
   incident. For Paddle: check the Paddle status page. If the
   provider reports an outage, the recovery is on their side —
   silence the alert (above) and wait.

2. **Wait one reconcile for a lagging provider.** A provider that
   accepted events but aggregates them late reads short until it
   catches up; the next reconcile tick (6 h) clears that drift. A
   push that failed never got a receipt, so it is not drift: the
   pusher replays it on its own. A receipt marks its hour delivered
   and the pusher does not send it again, so drift that survives a
   tick is an event the provider lost or doubled after accepting it.

3. **Escalate: contact provider support with the per-account
   query results.** For a per-account `BillingDriftAccount` that
   persists beyond a 7-day window, the delivered-vs-provider mismatch
   is real and unrecoverable from the box side. Open a support
   ticket with the provider, attach the `meterd_billing_drift_mb_seconds`
   series for that account, and request a manual replay of the
   missing usage records.

Recovery verification:

```bash
# After the provider recovers: the drift ratio should drop
# below 0.005 within one pusher cadence (1 h) + one reconcile
# cadence (6 h) = ~30 h. Verify with:
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=max by (provider) (meterd_billing_drift_ratio)' | jq .

# After a manual push replay: per-account drift ratio drops
# below 0.05 within one reconcile tick (~6 h).
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_billing_drift_ratio' | jq .
```

A sustained recovery (no further `BillingDrift` or
`BillingDriftAccount` fires for 24h) closes the incident; the
silence expires on its own and the gauge surfaces the next
genuine drift. Memory pressure is **not** part of this runbook —
`FaasHighResidentRamPct` governs memory; this runbook governs
provider-side billing surface only.
