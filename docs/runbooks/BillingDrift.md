# BillingDrift

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`,
the `BillingDrift` + `BillingDriftAccount` alert blocks
(ADR-049 §B.1).

Metrics: `meterd_billing_drift_mb_seconds{account_id, provider}`
(signed local − pushed) and `meterd_billing_drift_ratio{account_id,
provider}` (abs(drift) / max(local, pushed)) over the rolling
24 h window. Reconciliation failures are counted by
`meterd_billing_drift_reconcile_failures_total{provider,reason}`;
when non-zero, the gauges may be stale. Emitted by
`pkg/billing/reconciler`, which runs on a 6 h meterd cron tick
(default `FAAS_RECONCILE_INTERVAL`).

Severity: `page` (fleet-wide) and `warn` (per-account). The page
severity fires on `> 0.5%` fleet-wide drift for `> 1h` — a real
Polar, Paddle, or Stripe outage has the potential to silently lose
invoices. The per-account `warn` alert catches single-customer
drift early so an operator can investigate before the customer
notices.

## Symptom

The alert fires when the reconciler's drift ratio exceeds the
threshold over the rolling window:

| Alert | Trigger | For | Severity |
|---|---|---|---|
| `BillingDrift` | `max by (provider) (meterd_billing_drift_ratio) > 0.005` | 1h | page |
| `BillingDriftAccount` | `meterd_billing_drift_ratio > 0.05` | 15m | warn |
| `BillingReconcileFailures` | `rate(meterd_billing_drift_reconcile_failures_total[10m]) > 0` | 10m | page |

The `provider` label distinguishes Polar, Paddle, and Stripe. The
`account_id` label carries the offending customer id (omitted on
the fleet-wide rule, which aggregates by `provider`).

A sustained drift in one direction (e.g. local > pushed) means the
provider is missing usage records; the other direction (pushed >
local) is rarer and indicates duplicate pushes — both are revenue
or reputational risks.

## Verify

The reconciler is read-only against the provider surface. The
local-vs-pushed comparison runs in `pkg/billing/reconciler` on
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
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=meterd_billing_drift_ratio' | jq .

# Fleet-wide drift by provider (the page alert's source expression)
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=max by (provider) (meterd_billing_drift_ratio)' | jq .

# Signed drift in mb_seconds — direction tells you which side is
# under-reported
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=meterd_billing_drift_mb_seconds' | jq .

# Per-account detail — replace $ACCOUNT_ID
ACCOUNT_ID=<uuid>
curl -fsS --data-urlencode "query=meterd_billing_drift_mb_seconds{account_id=\"${ACCOUNT_ID}\"}" \
  'http://127.0.0.1:9090/api/v1/query' | jq .

# Provider reconciliation failures (the page alert's source)
curl -fsS --data-urlencode \
  'query=sum by (provider, reason) (increase(meterd_billing_drift_reconcile_failures_total[1h]))' \
  'http://127.0.0.1:9090/api/v1/query' | jq .
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

# Cross-check the local usage_minutes sum for the offending
# account (replace $ACCOUNT_ID)
ACCOUNT_ID=<uuid>
psql -t -A -c "select sum(mb_seconds) from usage_minutes where account_id = '${ACCOUNT_ID}' and minute >= now() - interval '24 hours'"
```

A drift paired with low `meterd_billing_drift_mb_seconds` per-account
count indicates a transient provider blip (Polar/Paddle/Stripe 5xx,
rate-limit) — fail-soft handles these; the next tick re-queries.
A drift paired with the same `account_id` firing repeatedly across
multiple ticks means the provider is missing records for that
customer — escalate.

A fleet-wide `BillingDrift` on one provider but not the others means
that provider is the affected surface. A fleet-wide drift on all
providers is rare and indicates an upstream Postgres issue (the local
sum would be the side most likely wrong) — check meterd's DB connectivity
first.

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

2. **Reconcile manually for the affected window.** If the provider
   is up but `meterd_billing_drift_mb_seconds` is non-zero,
   the meterd pusher automatically replays the durable usage windows.
   For a fleet-wide provider outage, the hourly pusher's idempotency-key
   contract means the next successful tick covers the gap — no manual
   replay needed. Cross-check by waiting one pusher interval (1 h) and
   re-querying the drift
   gauge; it should fall below the threshold.

3. **Escalate: contact provider support with the per-account
   query results.** For a per-account `BillingDriftAccount` that
   persists beyond a 7-day window, the local-vs-pushed mismatch
   is real and unrecoverable from the box side. Open a support
   ticket with the provider, attach the `meterd_billing_drift_mb_seconds`
   series for that account, and request a manual replay of the
   missing usage records.

Recovery verification:

```bash
# After the provider recovers: the drift ratio should drop
# below 0.005 within one pusher cadence (1 h) + one reconcile
# cadence (6 h) = ~30 h. Verify with:
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=max by (provider) (meterd_billing_drift_ratio)' | jq .

# After a manual push replay: per-account drift ratio drops
# below 0.05 within one reconcile tick (~6 h).
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=meterd_billing_drift_ratio' | jq .
```

A sustained recovery (no further `BillingDrift` or
`BillingDriftAccount` fires for 24h) closes the incident; the
silence expires on its own and the gauge surfaces the next
genuine drift. Memory pressure is **not** part of this runbook —
`FaasHighResidentRamPct` governs memory; this runbook governs
provider-side billing surface only.
