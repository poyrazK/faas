# FaasRetryAmplificationHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`retry_safety`.

Metrics: `gateway_retry_attempts_total{outcome}` and
`gateway_retry_exhausted_total{reason}`.

Severity: warn. Family: `retry_safety`.

## Symptom

Gateway-generated replay traffic has exceeded 20% of original attempts at
more than one replay per second for ten minutes. This normally means several
instances are failing in transport at once, or a retry rule was configured
with an unusually generous aggregate budget.

The gateway enforces both a per-request attempt ceiling and a short-window,
per-app aggregate allowance. A high `aggregate_budget` exhaustion rate means
the limiter is containing a larger failure; it is not itself a reason to
raise the budget.

## Verify

```bash
# Fleet replay/original ratio.
curl -fsS --data-urlencode 'query=sum(rate(gateway_retry_attempts_total{outcome=~"replay_ok|replay_failed"}[5m])) / clamp_min(sum(rate(gateway_retry_attempts_total{outcome=~"original_ok|original_failed"}[5m])), 0.001)' \
  http://127.0.0.1:9095/api/v1/query | jq .

# Which safety rule is declining retries?
curl -fsS --data-urlencode 'query=sum by (reason) (rate(gateway_retry_exhausted_total[5m]))' \
  http://127.0.0.1:9095/api/v1/query | jq .

# Transport failures and endpoint churn on gateway nodes.
journalctl -u gatewayd-internal --since '-20m' --no-pager | \
  grep -iE 'stale|quarantined|transport|retry|circuit'
```

Interpret the attempt outcomes together:

- `replay_ok` rising means healthy siblings are masking dead instances.
- `replay_failed` rising means siblings are failing too; investigate the
  compute node, vmmd transport, and app deployment.
- `aggregate_budget` rising means the limiter is actively preventing a retry
  storm. Do not increase the budget until the underlying failure is fixed.
- `missing_idempotency_key` means POST/PATCH replay was enabled but callers
  did not supply the required key.

## Recover

1. Identify and repair the failing gateway, compute node, vmmd bridge, or app
   deployment. Check circuit-breaker transitions and open-target counts.
2. Reduce `budget_percent`, `budget_min_retries`, or `max_attempts` on an
   over-generous `kind=retry` rule.
3. If retries are worsening an active incident, set `FAAS_GATEWAY_RETRY=0`
   for `gatewayd-internal` and roll/restart the service. This is the fleet
   kill switch for public edge replay. Internal service forwarding remains
   bounded by the shared aggregate limiter and its two-attempt ceiling.
4. Restore the flag only after replay traffic and transport failures have
   returned to baseline.

Do not solve this alert by increasing the aggregate retry budget. The budget
exists specifically to keep a broad target failure from multiplying load.
