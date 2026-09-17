# FaasAlertPresetAnyFiringAccount

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasAlertPresetAnyFiringAccount` in the
`faas_alert_preset_signals` group — the **correlation rule**).
Expression: `count by (account_id) (max by (account_id, preset)
ALERTS{alertstate="firing", family="alert_preset_signals", account_id!=""}) >= 1`
for 5m.
ADR: ADR-123 (alert_presets catalog).
Severity: warn (customer-facing catalog roll-up).

## Why this alert exists

When 1+ of the 5 alert_preset signals is firing for the same
account, this correlation rule aggregates them into a single
per-account alert so the customer's dashboard shows "this account
has at least one alert firing" without listing all 5 individual
signals. Operators investigating see this alert + the per-preset
alerts in parallel.

## Signal scope (load-bearing — read before responding)

The correlation consumes the five per-preset alert families directly;
their own `for:` hold periods are authoritative. The source families are:

- `preset="api_down"` (per `(account_id, app_id)`)
- `preset="spend_eur_20"` (per `account_id`)
- `preset="deploy_failed"` (per `(account_id, app_id)`)
- `preset="cert_expiring_14d"` (per `(account_id, app_id)`)
- `preset="queue_backlog_growing"` (per `(app, account_id)`)

As of PR-D the queue signal **contributes** to this correlation.
Pre-PR-D it was excluded because `gateway_queue_depth` carried
only the `app` label; PR-D adds `account_id` via the bounded-
admission set. For per-app queue alert mechanics, see
`docs/runbooks/FaasGatewayQueueBacklogGrowing.md`.

## Verify

```bash
# Which accounts have any preset firing?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=FaasAlertPresetAnyFiringAccount'

# For a specific account, list which per-preset alerts are ALSO firing:
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=ALERTS{alertstate%3D%22firing%22%2C+account_id%3D%22<acct>%22%2C+family%3D%22alert_preset_signals%22}'
```

The `max by (account_id, preset)` step collapses multiple apps firing
the same preset. The outer count reduces to a `1` or higher scalar per
account — the resulting alert shows only that **at least one preset is**
firing. Query the per-preset alerts directly for the active types.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasAlertPresetAnyFiringAccount' \
  --duration=1h \
  --comment='<acct> acknowledged; per-preset alerts separately silenced as needed'
```

> Note: silencing the correlation rule does NOT silence the
> per-preset alerts. If the goal is to silence the customer-facing
> view entirely, also silence each per-preset `severity=warn`
> alert with the same matcher prefix.

## Recover

The correlation clears when none of the five per-preset alert series
remain in `alertstate="firing"` for the correlation's 5m hold. A source
signal that is only pending does not activate this rollup; investigate
the per-preset alerts' own runbooks for recovery procedures.
