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

Three more opt-in, webhook-only security presets watch the edge for traffic that
is being rejected or would be:

- `pre_auth_pressure` fires when the [pre-auth source limit](security.md#optional-pre-auth-source-limit)
  blocks, or in observe mode would block, more than 100 requests in 15 minutes.
  The payload includes the same `observations_path` and `dashboard_path` as the
  login-target presets. New apps observe the pre-auth limit by default, so this
  alert can report pressure before you switch the guard to enforce.
- `edge_validation_failures` fires when `kind=validate` edge rules record more
  than 50 mismatched requests in 15 minutes, counting body, path, query, and
  header mismatches in every validate mode. Use the
  `gateway_validate_failures_total` metric or the `edge_rule.validate_failed`
  audit events to find the failing rule and field.
- `edge_rejection_pressure` fires when the other edge gates (JWT, IP, geo,
  ingress allowlist and internal-only, body limits, and throttles) answer more
  than 200 requests with 401, 403, 413, or 429 in 15 minutes. Gate outages
  (503) are not counted. Break the total down by gate with
  `gateway_edge_rejections_total{app="APP_ID"}`, whose `kind` and `status`
  labels name the gate and response.

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable pre_auth_pressure \
  --app APP_ID --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

Because outside traffic drives these signals, these alerts can never run a
deployment action such as rollback; the API and the database both reject one.

Deliveries include an event id, timestamp, alert state, and signature. Verify the signature before processing, deduplicate by event id, and return a 2xx response quickly. Retryable failures are retried with backoff; a permanently failing endpoint is paused so it cannot amplify an incident.

For dashboards and SLOs, use the app metrics endpoint and correlate alert event ids with deployment ids. Never put credentials in an alert URL.

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
