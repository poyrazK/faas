# FaasApiAvailabilityLow

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_requests_total{code=~"2.."}` vs total (gatewayd `/metrics`).
Spec: §12 (API availability 99.5% monthly).
Severity: page.

## Symptom

The 2xx ratio over the last 5 minutes has been below 99.5% for 10 m.
This is a rolling 5m reading — intentionally more sensitive than the
customer-facing monthly SLO (spec §12 row: "error budgets, not promises").
The status page (`/status/slo.json`) shows the same metric.

The page-tier severity reflects that the rolling breach is the
operator's leading indicator for the monthly SLO.

## Verify

```bash
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=sum(rate(gateway_requests_total{code=~"2.."}[5m]))/sum(rate(gateway_requests_total[5m]))'
curl -fsS https://DOMAIN/status/slo.json | jq .
```

## Check

```bash
journalctl -u gatewayd --since '-15m' --no-pager | grep -iE '5xx|panic|overt'
curl -fsS http://127.0.0.1:9090/api/v1/query?query='gateway_requests_total{code!~"2.."}' | head -100
```

A spike in `503` from gatewayd is the canonical sign of wake-queue
saturation (cap 512/30s per the CLAUDE.md gotcha). A spike in `502`
is upstream — apid, schedd, or vmmd refused the request.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasApiAvailabilityLow' \
  --duration=15m \
  --comment='incident bridge open; investigating'
```

## Recover

The rolling 5m window means a transient blip (deploy + restart) clears
within 10 minutes. A persistent breach requires the operator to
identify the failing component from `gateway_requests_total{code}`
labels and follow the per-daemon runbook.
