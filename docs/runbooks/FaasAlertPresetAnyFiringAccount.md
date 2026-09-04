# FaasAlertPresetAnyFiringAccount

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasAlertPresetAnyFiringAccount` in the
`faas_alert_preset_signals` group — the **correlation rule**).
Expression: `count by (account_id) (<5 boolean predicates over the
account-labeled alert_preset recordings>) >= 1` for 5m.
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

The 5 boolean predicates inside the correlation are:

- `faas_api_down_over_5m:by_account_app < 1` (per `(account_id, app_id)`)
- `faas_spend_eur_20_over_24h:by_account > 20` (per `account_id`)
- `faas_deploy_failed_over_1h:by_account_app > 0` (per `(account_id, app_id)`)
- `faas_cert_expiring_14d_over_24h:by_account_app < 1209600` (per `(account_id, app_id)`)
- `faas_queue_backlog_growing_over_15m:by_app_account > 50` (per
  `(app, account_id)` — admitted via `accountLabelSet`,
  overflow=`__other__`)

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

The `count by (account_id) (...) >= 1` reduces to a `1` or higher
scalar per account — the resulting alerts do NOT show "how many
of the 5 are firing" as a number; they show only that **at least
one is**. To get the breadth, query the per-preset alerts directly.

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

The correlation clears when none of the 5 boolean predicates
hold for any matching row over the `for:` window (5m). That
usually means the underlying per-preset signal cleared on its
own — investigate the per-preset alerts' own runbooks for
recovery procedures.
