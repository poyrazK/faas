# FaasApiAvailabilityLow

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_platform_errors_total{app!="-"}` (5xx the gateway produced before any guest response byte) vs resolved-app 2xx+5xx `gateway_requests_total` (gatewayd-internal `/metrics`). Requests labelled `app="-"` never resolved to a tenant route and are excluded. A 5xx the customer's app returned itself is not platform unavailability and does not count.
Spec: §12 (API availability 99.5% monthly).
Severity: warn (ADR-1236). `FaasApiAvailabilityBurnRateHigh` is the page.

## Symptom

The 2xx ratio over the last 5 minutes has been below 99.5% for 10 m.
This is a rolling 5m reading — intentionally more sensitive than the
customer-facing monthly SLO (spec §12 row: "error budgets, not promises").
The status page (`/status/slo.json`) shows the same metric.

The page-tier severity reflects that the rolling breach is the
operator's leading indicator for the monthly SLO.

## Verify

```bash
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=1-sum(rate(gateway_platform_errors_total{app!="-"}[5m]))/sum(rate(gateway_requests_total{app!="-",code=~"2..|5.."}[5m]))'
# Multi-window burn-rate check (14.4x / 6x of the 99.5% monthly budget).
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum(rate(gateway_platform_errors_total{app!="-"}[1h]))/sum(rate(gateway_requests_total{app!="-",code=~"2..|5.."}[1h]))'
# Which apps the platform failed (app-authored 5xx are in gateway_requests_total only).
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk(10,sum by (app)(increase(gateway_platform_errors_total[1h])))'
curl -fsS https://api.gregale.dev/status/slo.json | jq .
```

## Check

```bash
journalctl -u faas-gatewayd-public faas-gatewayd-internal --since '-15m' --no-pager | grep -iE '5xx|panic|overt'
curl -fsS http://127.0.0.1:9095/api/v1/query?query='gateway_requests_total{code!~"2.."}' | head -100
```

A spike in `503` from gatewayd-internal is the canonical sign of wake-queue
saturation (cap 512/30s per the CLAUDE.md gotcha). A spike in `502`
is upstream — apid, schedd, or vmmd refused the request.

## Silence

```bash
amtool silence add \
  --matchers='alertname=~"FaasApiAvailability(Low|BurnRateHigh)"' \
  --duration=15m \
  --comment='incident bridge open; investigating'
```

## Recover

The rolling 5m window means a transient blip (deploy + restart) clears
within 10 minutes. A persistent breach requires the operator to
identify the failing component from `gateway_requests_total{code}`
labels and follow the per-daemon runbook.
