# FaasResidentGbPerCustomerHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `meterd_resident_gb_per_customer{plan}` (meterd `/metrics`).
Spec: §12 (resident GB per paying customer — plan target 0.305, warn > 0.45).

## Symptom

The §12 fleet dashboard's "Resident GB per paying customer" panel
shows a single plan crossing the 0.45 warn threshold for 1 hour.
Plan label is in the alertname annotation: `{{ $labels.plan }}`.

The metric is monthly GB-RAM-hours divided by the paying-customer
count of the plan, emitted by meterd's residency tick (every 60 s,
`pkg/meter/residency.go`). "Paying" is `active | past_due | suspended`
(per ADR-031) — the deliberate divergence from `state.Account.Active()`
because suspended accounts still have running instances until the
reaper parks them, and their GB-hours are real platform cost.

## Verify

```bash
curl -fsS http://127.0.0.1:9106/metrics | grep meterd_resident_gb_per_customer
```

Each plan label should appear with a current value. If a single plan
is the offender, look at the per-app consumers in that plan:

```bash
curl -fsS http://127.0.0.1:9106/metrics | grep -E '^meterd_resident_gb_per_customer{'
curl -fsS -G http://127.0.0.1:9090/api/v1/query \
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

The metric is informational — the page-tier consequence is
`FaasHighResidentRamPct` (fleet RAM ceiling, 92% page). If both fire,
follow the high-resident-RAM runbook. If only this one fires, identify
the offending app(s) via the per-account breakdown above and either:

1. Adjust the customer's idle timeout / max concurrency via
   `apid PATCH /v1/apps/{slug}` (operator action — Pro/Scale only).
2. Wait for monthly GB to drop — usage resets at month-end UTC.
3. Migrate the customer to a higher plan via Stripe self-service.
