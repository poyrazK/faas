# FaasApiDown

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasApiDownAccount` in the `faas_alert_preset_signals` group).
Metric: `meterd_api_reachable{account_id, app_id}` — gauge set to
1.0 by `cmd/meterd/api_reachability_sweep.go` on a successful
invocation over the rolling window, else 0.0.
ADR: ADR-123 (alert_presets catalog). Catalog preset: `api_down`,
window 5m, threshold 1.0 (reachable).
Severity: warn (customer-facing catalog row).

## Symptom

The reachability gauge for a customer's app has been below 1.0
for 5m. The preset is the **customer-facing** counterpart of the
platform-tier `FaasApiAvailabilityLow` (line ~316 of
`faas.rules.yml`) — `FaasApiAvailabilityLow` rolls the whole fleet
into a page-tier 99.5% breach, while `FaasApiDownAccount` fires
per-account as a ticket-tier signal so customers see their own
state in the dashboard.

The 5m `for:` window matches the catalog's `window_spec` —
transient wake failures (cold boot snapshot restore retry, network
init race) clear within one window and never fire.

## Verify

```bash
# Which (account, app) is unreachable?
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=meterd_api_reachable%20%3C%201'

# What's the customer's spend? (cross-check for runaway billing)
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=meterd_account_spend_eur{account_id%3D%22<acct>%22}'

# Is gatewayd-internal queue saturated for the same app?
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=gateway_queue_depth{app%3D%22<app>%22}'
```

Common causes:

1. **Customer app crashed and is restarting** — `meterd_api_reachable`
   drops to 0 during the cold-boot latency. Should clear in
   <350 ms + 5m `for:`.
2. **gatewayd-internal queue saturated** — cross-check
   `FaasQueueBacklogGrowingApp`. Queue cap is 512/30s per the
   CLAUDE.md gotcha; if crossed, wake latency exceeds the queue
   hold window and reachability drops.
3. **Customer's app slug typo'd at deploy time** — common after
   `rename` / `redeploy`. Check `cmd/apid/deployment_pipeline.go`
   for the latest deployment status.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasApiDownAccount' \
  --duration=30m \
  --comment='investigating customer app cold-boot; reached out to <acct>'
```

## Recover

The alert clears automatically once the gauge is ≥ 1.0 for the
duration of the `for:` window (5m) and a 1-2m `prober` re-eval
rounds. No operator action required to clear — only to silence
during planned customer-side maintenance.
