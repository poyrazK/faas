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
  labels name the gate and response. When a flood comes from more sources
  than rules can keep up with, `gregale edge-rules attack-mode on` challenges
  every visitor for a set time ([attack mode](security.md#attack-mode)).
- `edge_waf_detections` (Pro and above) fires when `kind=waf` edge rules detect
  more than 25 likely attacks in 15 minutes, counting sampled detections and
  the in-path verdicts of `warn` and `block` rules. Only `block` rules stop
  requests, and only on headers and URL; those 403s also count toward
  `edge_rejection_pressure`.
  Body detections are a sample: each app's inspections are budgeted and
  requests with bodies are expensive to inspect, so under load only part of
  the matched traffic is inspected (the summary's `not_inspected` count).
  `gregale edge-rules summary` lists detections by attack category and the
  CRS rule IDs that scored most; add a rule ID to the rule's
  `exclude_rule_ids` when it fires on legitimate traffic.

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts preset enable pre_auth_pressure \
  --app APP_ID --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

To see the same counts on demand, run `gregale edge-rules summary --app APP
--range 24h`, open the app's edge-rules dashboard page (Edge protection panel),
or call `GET /v1/apps/{slug}/edge-protection?range=24h`.

Because outside traffic drives these signals, these alerts can never run a
deployment action such as rollback; the API and the database both reject one.

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

Point a rule at channels with `--channel` (repeatable, up to five). A rule
with at least one channel needs no webhook:

```bash
gregale alerts add --app shop --name "checkout errors" --metric error_rate_pct \
  --comparison gt --threshold 5 --window-spec 5m --channel OPS_SLACK_ID --channel ONCALL_PD_ID
```

Channels receive the fire and, when the rule recovers, a resolve: PagerDuty
closes the incident the fire opened, and Slack and email get a "Resolved"
message. Each channel is delivered to independently, so a broken Slack
webhook never delays PagerDuty, the rule's webhook, or a rollback action.
Presets take channels too: `gregale alerts preset enable availability --app
shop --channel OPS_SLACK_ID`.

Deleting a channel removes it from every rule; a rule left with no webhook
and no channel stops notifying. The app dashboard's Alerts panel shows where
each rule delivers ("Delivers to") and lists every channel with its last
delivery or error, so a rule delivering nowhere or a revoked Slack webhook is
visible before an incident.

### What the logs show

Where log search is enabled (preview), an app alert that fires also says what
the app's logs show over the alert window (up to 24 hours), so you can tell at
a glance whether something is erroring and whether something just shipped:

```text
Logs in the window: 132 errors, 18 warnings, 87 HTTP 5xx (most errors on /checkout: 91)
deploy traffic changed from 10% to 100% at 14:02 UTC
Search the logs: https://…/dashboard/apps/shop/log-search?since=…&until=…&level=error%2Cfatal
```

Slack, PagerDuty and email show these lines on the fire; webhooks receive the
same data as `log_context` (`errors`, `warnings`, `http_5xx`,
`busiest_error_route`, `recent_deploys` from the window and the hour before
it, and `search_path`). Resolve messages and account-wide rules carry none,
and if the logs cannot be read in time the alert is delivered without them.

## Alert on unusual values

A fixed threshold has to be chosen in advance: 1% errors is an emergency for
one app and a normal day for another. A baseline rule instead compares a metric
with its usual value for that app at the same time of day, so you can alert on
"three times the normal error rate" without picking a number.

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app my-api --name errors-unusual \
  --metric error_rate_pct --comparison above_baseline --threshold 3 --window-spec 15m \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin

# Traffic dropped to under a third of normal, for example a broken client release.
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app my-api --name traffic-drop \
  --metric request_count --comparison below_baseline --threshold 0.3 --window-spec 1h \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

The usual value is the median of the same metric, over the same window length,
at the same time of day on each of the previous seven days. With
`above_baseline` the threshold is a multiplier from 1.5 to 20; with
`below_baseline` (request count only) it is a fraction from 0.05 to 0.67.
Supported metrics are `error_rate_pct`, `latency_p95_ms`, `latency_p99_ms`,
`cold_start_pct`, and `request_count`.

To avoid alerts on tiny numbers, a baseline rule does not fire when:

- the window had fewer than 50 requests;
- the value is below a sensible minimum (1% errors, 50 ms latency, 5% cold
  starts) even if it is several times the usual value;
- the usual value is zero;
- fewer than five of the previous seven days had traffic, for example for a new
  app. The rule shows as unknown until enough history exists.

The webhook payload adds `baseline` (the usual value), `baseline_days`, and
`ratio` (observed divided by baseline) to the usual fields. Baseline rules are a
preview (ADR-744) and must be enabled for your deployment.
## Log alerts

`log_error_lines` and `log_warn_lines` count the lines your app wrote at
error or warning level during the window, across every instance and node:

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "error log burst" \
  --metric log_error_lines --comparison gt --threshold 50 --window-spec 5m \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

A line's level comes from a structured `level` or `severity` field when the
line is JSON, otherwise from common prefixes such as `[ERROR]`, `level=error`
or `[warn]` in the first 512 bytes. This is the same classification as
`gregale logs --level error`, so the command shows the lines an alert
counted. A line that only mentions "error" in prose is not counted. Build
logs are never counted. Log alerts support every alert action, including
rollback, and every window.

## Queue backlog alerts

`queue_backlog` is the number of messages waiting in the app's queues or held
by a worker, and `queue_oldest_age_seconds` is how long the oldest waiting
message has waited. Use them to hear about a [worker](workers.md) that falls
behind:

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app my-worker --name "jobs backing up" \
  --metric queue_oldest_age_seconds --comparison gt --threshold 600 --window-spec 15m \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

Both compare the latest reading, refreshed every 30 seconds. The backlog
count stops at 100,000, and an empty queue reads 0. These rules are
webhook-only. `queue_depth` is a different signal: requests the gateway holds
while an instance wakes.

### Log search alerts

Where log search is enabled (preview), `log_search_matches` counts the log
events matching a set of [log search](log-search.md) filters during the
window: 5xx responses on one route, runtime errors mentioning the database,
failed builds. Pass the filters as `log_query`, or name a saved search and the
CLI copies its filters into the rule:

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "checkout 5xx" \
  --metric log_search_matches --saved-search checkout-5xx --comparison gt --threshold 20 \
  --window-spec 15m --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin

printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "database errors" \
  --metric log_search_matches --log-query 'source=runtime&level=error&q=database' \
  --comparison gt --threshold 0 --window-spec 1h \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

The rule keeps its own copy of the filters, so editing or deleting the saved
search later does not change it. Counts include every occurrence of a
deduplicated event. These rules are webhook-only and support windows up to
`24h`. If the search cannot finish in time, the rule is `unknown` for that
evaluation rather than firing or clearing.
## SLO alerts

`slo_budget_burn` and `slo_budget_remaining_pct` watch one of the app's own
SLOs, named with `slo_id`. See [SLOs and error budgets](slos.md#alert-on-an-slo).
## Synthetic check alerts

`synthetic_check_consecutive_failures` and `synthetic_check_latency_p95_ms`
watch one of the app's synthetic checks, named with `synthetic_check_id`. See
[synthetic checks](synthetic-checks.md#alert-on-a-check).

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

## Custom metric alerts

Where custom metric history is enabled (preview, ADR-745), you can alert on a
number you push yourself — orders waiting, failed payments, documents awaiting
OCR. Name the metric with `custom_metric_name` (`--custom-metric` in the CLI):

```bash
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "orders backing up" \
  --metric custom_metric --custom-metric orders_pending --comparison gt --threshold 1000 \
  --window-spec 15m --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

A gauge is compared on its average over the window, so `5m` follows the
current value and `1h` asks whether it stayed there. A counter (an
OpenTelemetry cumulative sum) is compared on its per-second rate over the
window. If nothing was pushed in the window, or the metric was deleted, the
rule is `unknown` and does not fire: a stopped pusher never satisfies a "below"
rule by silence. These rules are webhook-only and support windows up to `24h`.
See [custom metrics](scaling-policy.md#custom-metrics) for pushing values.
