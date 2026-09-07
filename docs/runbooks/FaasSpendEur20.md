# FaasSpendEur20

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasSpendEur20Account` in the `faas_alert_preset_signals`
group).
Metric: `meterd_account_spend_eur{account_id}` — gauge rolled up
from `meterd_invocations_billed_total` per the spec §4.7 billing
formula (plan RAM + 8 MB × running seconds, integer cents).
ADR: ADR-123 (alert_presets catalog). Catalog preset:
`spend_eur_20`, window 24h, threshold 20.0 EUR.
Severity: warn (customer-facing catalog row).

## Symptom

The customer's rolling 24h spend has crossed 20 EUR and stayed
above for 1h. The 1h `for:` matches the catalog's
`default_cooldown_minutes` so transient blips (an 8-hour deploy
that briefly doubled invocations) clear without a page.

This is the **customer-facing** counterpart of the platform-tier
`FaasHighResidentRam` / `FaasResidentGbPerCustomerHigh` family
(those fire on average resident GB, not on EUR). The customer
contract (§11 financial model) bills on plan RAM + 8 MB per
running second, NOT sampled RSS — so the EUR gauge is the
authoritative billing signal.

## Verify

```bash
# What is the customer actually spending?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_account_spend_eur{account_id%3D%22<acct>%22}'

# How many invocations in the last 24h?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_invocations_billed_total{account_id%3D%22<acct>%22}'

# Cross-check per-account rate-limit — are they being throttled?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_account_rate_limit_rejected_total{account_id%3D%22<acct>%22}'
```

Common causes:

1. **Plan tier crossed** — Free (1/1/128/5) → Hobby (5/2/256/50)
   crossover. Customer likely exceeded their plan limit and
   incurred overage at €0.01/GB-h.
2. **Runaway invocations** — a cron misfire, an external client
   hammering the customer's app, or an infinite-loop bug. Check
   `meterd_invocations_billed_total` rate — if it's >> the typical
   24h average, the customer has a runaway client.
3. **Cold-boot storm** — many parked instances woke in a burst.
   Snapshot restore latency ≈ 350 ms × N concurrent wakes, so a
   burst of N=50 wakes adds ~17.5 s of billable running-seconds
   per instance × 50.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasSpendEur20Account' \
  --duration=4h \
  --comment='customer <acct> opted into a free-tier top-up; observing'
```

## Recover

The alert clears automatically once the rolling 24h spend drops
back below 20 EUR and the `for:` window expires. If the customer
is the source of runaway invocations, page them through the
dashboard's support contact channel BEFORE silencing — the
billing team will need a paper trail for any refund request
processed outside the customer's plan tier.
