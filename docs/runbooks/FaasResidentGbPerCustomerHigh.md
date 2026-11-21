# FaasResidentGbPerCustomerHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `meterd_resident_gb_per_customer{plan}` and
`meterd_resident_gb_per_customer_planned{plan}` (meterd `/metrics`).
Spec: §12 (resident GB per paying customer), ADR-20261117070932.

## Symptom

One plan's average resident GB per paying customer has been above 1.5×
its planned value for 1 hour, with at least five paying customers. The
plan is the alert's `plan` label.

The planned value is the financial model's resident concurrency for the
plan times its RAM plus 8 MB (`api.PlannedResidentGBPerCustomer`): Free
0.005, Hobby 0.076, Pro 0.605, Scale 6.02 GB. The §12 0.305 GB target is
the blend across the plan mix, so a Pro or Scale cohort normally reads
above it; that is not this alert.

The metric is month-to-date GB-RAM-hours divided by elapsed hours in
the UTC month and by the paying-customer count of the plan. This yields
average resident GB per customer. It is emitted by meterd's residency
tick (every 60 s, `pkg/meter/residency.go`). "Paying" is
`active | past_due | suspended`
(per ADR-031) — the deliberate divergence from `state.Account.Active()`
because suspended accounts still have running instances until the
reaper parks them, and their GB-hours are real platform cost.

## Verify

```bash
curl -fsS http://127.0.0.1:9106/metrics | grep meterd_resident_gb_per_customer
```

Each plan label should appear with a current value, and the
`_planned` series with the plan's model. If a single plan
is the offender, look at the per-app consumers in that plan:

```bash
curl -fsS http://127.0.0.1:9106/metrics | grep -E '^meterd_resident_gb_per_customer{'
curl -fsS -G http://127.0.0.1:9095/api/v1/query \
  --data-urlencode 'query=topk(10, sum by (app_id) (usage_minutes{mb_seconds>0}))' \
  | jq '.data.result[] | {app: .metric.app_id, gb: .value[1]}'
```

## Check

```bash
# Per-account breakdown for the offending plan
psql -U faas -d faas -c "
SELECT account_id, plan, SUM(mb_seconds) AS monthly_mb_seconds
FROM usage_minutes
WHERE month = date_trunc('month', now())
GROUP BY account_id, plan
ORDER BY monthly_mb_seconds DESC
LIMIT 20;"
```

A few heavy accounts usually dominate the per-plan average — a Hobby
customer running a long-lived crawler, a Scale customer with
min_instances set. Identify the top 5 consumers and look at their
idle timeout + min_instances configuration.

## Silence

```bash
amtool silence add \
  --matchers='alertname="FaasResidentGbPerCustomerHigh",plan="hobby"' \
  --duration=1h \
  --comment='hobby migration in progress — see OPS-1234'
```

Silence per-plan (not blanket) so a real Scale-tier breach isn't
suppressed alongside a known Hobby migration.

## Recover

The metric is an economic signal, not a capacity one: the plan's
customers are running more than its price assumes. Node memory pressure
pages separately (`FaasHighResidentRamPct`). Identify the offending
app(s) via the per-account breakdown above and either:

1. Adjust the customer's idle timeout / max concurrency via
   `apid PATCH /v1/apps/{slug}` (operator action — Pro/Scale only).
2. Reduce unnecessary residency and let the month-to-date average fall.
3. Move the customer to a higher plan through the configured billing
   provider's customer portal or checkout flow. Polar is the public-release
   default; Paddle and Stripe remain compatibility integrations.
