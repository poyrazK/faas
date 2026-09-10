# FaasApiDown

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasApiDownAccount` in the `faas_alert_preset_signals` group).
Metric: `faas_api_down_over_5m:by_account_app` — a recording rule
derived from `gateway_requests_total`. It emits 0 only when an app
received public traffic in the rolling 5m window without a 2xx
response, 1 when at least one 2xx response completed, and no series
when the app was idle. `meterd_api_reachable` supplies the bounded
`app_id` to `account_id` label mapping; its async-invocation value is
not used as the public HTTP health signal.
ADR: ADR-123 (alert_presets catalog). Catalog preset: `api_down`,
window 5m, threshold 1.0 (reachable).
Severity: warn (customer-facing catalog row).

## Symptom

An app has continued to receive public traffic without any 2xx
response for 5m. The preset is the **customer-facing** counterpart of the
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
# Which active (account, app) has no successful public response?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=faas_api_down_over_5m%3Aby_account_app%20%3C%201'

# What's the customer's spend? (cross-check for runaway billing)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_account_spend_eur{account_id%3D%22<acct>%22}'

# Is gatewayd-internal queue saturated for the same app?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=gateway_queue_depth{app%3D%22<app>%22}'
```

Common causes:

1. **Customer app crashed or wake repeatedly failed** — confirm the
   failing status classes in `gateway_requests_total` and inspect
   wake failure metrics for the app's node.
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

The alert clears on the next successful 2xx response or when the app
has no requests left in the rolling 5m window. No operator action is
required to clear it.
