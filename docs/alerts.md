# Alerts

Alerts turn platform signals into signed webhook deliveries. Configure them per account or app and keep the receiver idempotent.

## Configure an alert

```bash
gregale alerts preset list
# Choose one enable command; action defaults to webhook.
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable availability --app APP_ID --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
# Safe-release actions are available when a preset should trigger a rollout response.
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable availability --app APP_ID --action rollback --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
gregale alerts list --app APP_ID
gregale alerts rm --app APP_ID ALERT_ID
```

Useful presets include availability, latency, error rate, out-of-memory, certificate expiry, quota, and spend. Use the preset as a starting point; narrow the threshold and notification window in the generated configuration when needed.

For automations, the opt-in `automation_backlog` preset sends a webhook when
due work has waited at least five minutes. It covers overdue timers, event or
callback timeouts, expired worker leases and concurrency-blocked runs. Future
waits, retry backoff and live execution do not count. Its 30-minute default
cooldown limits repeat notifications; age is current state rather than a
window average. The preset supports webhook notifications only.

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable automation_backlog \
  --app billing --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

Use `workflow_due_age_seconds` for a custom threshold in seconds. Inspect
[automation health](automation-authoring.md#inspect-automation-health) and run
history to find waiting reasons. Enable this preset after the backlog-alert
migration and evaluator upgrade; it does not enable itself for existing apps.

For login abuse, `login_target_pressure` is an opt-in security preset for apps that have enabled `observe_targets` on a pre-auth POST route. It sends a webhook when the aggregate target-threshold signal exceeds five events in 15 minutes. The alert includes `observations_path` for the API and `dashboard_path` for the read-only pre-auth protection view. It never includes the login target digest. This preset supports webhook notifications only.

`login_target_signal_health` is a second opt-in, webhook-only preset. It fires
when more than 10% of selected failed responses on any observed route have a
missing or invalid target digest over 15 minutes, provided that route has at
least 20 selected failures. The rule's `observed` value is the highest eligible
route's missing/invalid percentage; the dashboard link shows which route needs
attention. If every route has fewer than 20 failures, the rule is `unknown` and
does not send a webhook. A Prometheus error sets it to `degraded`, also without
firing. Neither state proves the integration is healthy.

Deliveries include an event id, timestamp, alert state, and signature. Verify the signature before processing, deduplicate by event id, and return a 2xx response quickly. Retryable failures are retried with backoff; a permanently failing endpoint is paused so it cannot amplify an incident.

For dashboards and SLOs, use the app metrics endpoint and correlate alert event ids with deployment ids. Never put credentials in an alert URL.

## Notification channels

Channels deliver alerts to Slack, PagerDuty or email without a webhook
receiver of your own (ADR-749). Add a channel once per account, then send a
test:

```bash
# Slack: create an incoming webhook in Slack, then paste its URL.
printf '%s\n' "$SLACK_WEBHOOK_URL" | gregale channels add --name ops-slack --kind slack --secret-stdin
# PagerDuty: an Events API v2 integration key from the service's Integrations tab.
printf '%s\n' "$PD_ROUTING_KEY" | gregale channels add --name oncall --kind pagerduty --secret-stdin --pagerduty-region eu
# Email: your account's own address.
gregale channels add --name me --kind email --email you@example.com

gregale channels test CHANNEL_ID
gregale channels list
```

- Slack URLs must be `https://hooks.slack.com/services/…`; PagerDuty
  notifications go only to PagerDuty's Events API. Both destinations are
  sealed at rest and never shown again; `list` shows a short hint instead.
- Email channels can only send to the account's own email address for now.
- `list` shows each channel's last successful delivery or last error, so a
  revoked Slack webhook shows up before an incident does.

Binding channels to alert rules arrives in the next release; until then a
channel can be tested but receives no alerts.

## Event consumer routing alerts

Consumer health rules require an app subscription UUID in the immutable
`event_subscription_id` field (`--event-subscription-id` in the CLI), a window
up to 24h, and webhook action. They observe backlog, retries scheduled, terminal
routing failures, routing latency, continuous pause duration, and drain rate.
Paused consumers suppress all of these except the pause-duration metric.
Compacted history and insufficient terminal samples skip rate evaluation.
Drain-rate rules skip idle consumers. See [consumer health](event-driven.md#consumer-routing-health-and-maintenance-alerts)
for metric names, units, and sample thresholds.

Consumer execution alerts use `--event-subscription-id` with
`event_execution_dead_letters`, `event_execution_dead_letter_rate_per_second`,
`event_handler_failure_pct`, or `event_completion_latency_p95_seconds`. These
webhook-only rules observe retained admitted executions, including handler
replays. Supported windows are `5m`, `15m`, `1h`, `6h`, and `24h`. Unlike routing
alerts, they remain active during subscription pauses. Truncated observations,
missing roots, unknown states, or insufficient samples produce `unknown`.
Failure percentages require 20 retained success/failure attempts, and completion
latency requires one success. See [consumer execution health](event-driven.md#consumer-execution-health)
for retention coverage and the distinction between backlog and formation rate.
