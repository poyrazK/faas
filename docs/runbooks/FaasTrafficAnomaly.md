# FaasTrafficAnomaly

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`,
`faas_anomaly_baseline` recording-rule group + 4 alert blocks in
`faas_slo` (issue #303, ADR-039).

Metric: `apid_request_total{account_id, route, code}` (paired with
`apid_request_failures_total{account_id, route, code}` for the
per-account error-rate view, `code="err"` invariant, issue #278).

Severity: page (`mode=fleet|platform`), warn (`mode=account`). The
`family` label is `traffic_anomaly`, so the existing `family`-based
inhibition / silencing rules compose with this alert.

## Symptom

The page fires when one of the alert conditions holds for the
configured `for:` window:

| Alert | Mode | Direction | Trigger |
|---|---|---|---|
| `FaasTrafficSpike` | fleet | spike | `max(faas_apid_request_rate_ratio:by_route) > 3` for 10m |
| `FaasTrafficDrop` | fleet | drop | a route < 0.2x its 3d baseline AND > 0.1 rps for 15m |
| `FaasErrorRateSpike` | fleet | spike (error_rate) | `max(faas_apid_error_rate_ratio:by_route) > 2` for 10m |
| `FaasErrorRateDrop` | fleet | drop (error_rate) | a route < 0.5x its 3d baseline AND > 0.001 err/s for 15m |
| `FaasTrafficAnomaly` | platform | drift | 10+ accounts simultaneously > 2× their 3d baseline for 10m |
| `FaasTrafficSpikeAccount` | account | spike | a customer > 10x its own 3d baseline for 15m |
| `FaasTrafficDropAccount` | account | drop | a customer < 0.1x its own 3d baseline AND > 0.1 rps for 30m |

The 3d baseline is calculated by the `faas_anomaly_baseline` recording-rule
group; the alert rules read the recording rules for the fleet-wide
variants and inline `avg_over_time` for the per-account variants
(see ADR-039 §Consequences for why).

`account_id="__other__"` is the bounded overflow bucket (issue #278)
— drill-down on this means the customer is past the 10 000 admission
cap and the operator must check the daemon slog for the original id.
The `FaasTrafficAnomaly` page explicitly excludes it via
`unless on (account_id)`.

## Verify

The dashboard `faas-fleet-m8` has dedicated panels (ids 80, 81, 82, 83,
90 in the JSON) that read from the recording rules. For ad-hoc
Prometheus API queries:

```bash
# Fleet-wide drill-down — what's the current vs 3d ratio per route?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=faas_apid_request_rate_ratio:by_route' | jq .

# Per-route error-rate ratio (current vs 3d)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=faas_apid_error_rate_ratio:by_route' | jq .

# Per-account anomaly score — how many accounts are above 2x?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=count(faas_apid_anomaly_score:by_account > 2)' | jq .

# Per-account drill-down — replace $ACCOUNT_ID with the offending uuid
ACCOUNT_ID=<uuid>
curl -fsS --data-urlencode "query=sum by (route) (rate(apid_request_total{account_id=\"${ACCOUNT_ID}\"}[5m]))" \
  'http://127.0.0.1:9095/api/v1/query' | jq .
```

## Check

```bash
# Sustained 5xx from a single op = upstream cause
curl -fsS http://127.0.0.1:9095/api/v1/query?query='apid_ops_total{code="err"}' | head -200

# Per-customer failure investigation
curl -fsS http://127.0.0.1:9095/api/v1/query?query='apid_request_failures_total' | jq .

# Recent daemon logs (search for the offending route or account_id)
journalctl -u apid --since '-15m' --no-pager | grep -iE '5xx|panic|overt|account_id='
```

A spike paired with low `apid_request_failures_total` is a scanner
(many 200s + a small 4xx tail). A spike paired with high
`apid_request_failures_total{account_id="<uuid>"}` is a real outage
for a specific customer — follow the per-account failure runbook
(`FaasApidAuditWriteFailures.md`) for triage.

A drop paired with rising `apid_ops_total{code="err"}` is the
canonical upstream-outage pattern; check
`FaasApiAvailabilityLow.md` first.

## Silence

```bash
# Silence all 4 traffic-anomaly alerts
amtool silence add \
  --matchers='alertname=~"FaasTrafficSpike.*|FaasTrafficDrop.*"' \
  --duration=30m \
  --comment='incident bridge open; investigating'

# Silence just the per-account variants (e.g. expected customer
# maintenance window)
amtool silence add \
  --matchers='alertname=~"FaasTrafficSpikeAccount|FaasTrafficDropAccount"' \
  --duration=15m \
  --comment='planned customer maintenance; suppress per-account alerts'
```

## Recover

**Spike (fleet or account)**: the most common cause is a runaway
retry storm (a customer deploys a misconfigured client that
re-fetches on every failure). The fix is to identify the
offending account_id and coordinate a per-account block via the
§11 egress catalog; the block surfaces in the
`apid_request_total{account_id="__other__",route=...}` series
after a couple of minutes, confirming the suppression is taking
effect. Cross-check the upstream cause via
`apid_ops_total{code="err"}` — if failures are low, it's a
retry storm; if failures are high, it's an outage.

**Drop (fleet or account)**: the most common cause is an upstream
outage (Postgres, gatewayd-public, gatewayd-internal, schedd). Cross-check
`FaasApiAvailabilityLow` and `FaasResidentGbPerCustomerHigh` for
fleet-wide pressure. For per-account drops that don't have a
matching fleet-wide signal, the customer is likely in maintenance
or has just churned — check the deploy log for the account_id.

**Error-rate spike / drop (`FaasErrorRateSpike`, `FaasErrorRateDrop`)**:
error-rate deviations without a corresponding rate change usually
mean the upstream got slower (5xx latencies doubled) or a tenant
started hitting a new error class. Read the per-route error-rate
ratio (`faas_apid_error_rate_ratio:by_route`) to find the
offending route, then drill into `apid_request_failures_total{code="err"}`
to confirm the per-account distribution. Often a transient
Postgres-statement contention or a new tenant app rolling out
broken code; resolve via the standard recovery in
`FaasApiAvailabilityLow.md`.

**Platform-wide drift (`FaasTrafficAnomaly`)**: 10+ accounts
simultaneously crossing 2× their 3d baseline is rare — almost
always either a global scan/burst, a misconfigured dashboard /
refresh loop in a popular customer app, or a deploy that flipped
a feature flag and triggered cascading re-fetches. Triage:
1. Read the "Anomaly spikes (last 7d)" panel (id 90) for the
   top-20 accounts by peak anomaly score; if the top accounts
   are all on the same route, it's a fleet-side issue (post-deploy
   regression); if they're on different routes, it's likely
   customer-side coordination (less common — confirm with the
   customer success team).
2. Cross-check `apid_request_failures_total{code="err"}` — if the
   error rate is also elevated, it's likely an outage; if not,
   it's a successful scan/burst.
3. If it's a fleet-side post-deploy regression, identify the
   daemon via the route's `topk` panel (id 82) and roll back the
   most recent apid / gatewayd-public / gatewayd-internal deploy. If it's a customer-side
   issue, coordinate a temporary per-account block via the §11
   egress catalog.
