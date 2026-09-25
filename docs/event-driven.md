# Event-driven workloads

Use asynchronous invokes, jobs, webhooks, and scheduled triggers when work does not need to finish in the request path.

```bash
gregale invoke --async --payload @payload.json APP_ID
gregale invoke --async --on-success-webhook WEBHOOK_ID --on-failure-webhook DLQ_WEBHOOK_ID APP_ID
gregale jobs run nightly --tasks 10
gregale crons add --app APP_ID --schedule "0 * * * *" --path /jobs/nightly
gregale crons add --app APP_ID --schedule "*/15 * * * *" --command bin/maintenance --arg=--compact
gregale jobs add nightly-export --image registry.example/exporter:v1 --schedule "0 3 * * *" --timezone Europe/Istanbul
```

Use a scheduled job when work should run to completion in an isolated job
environment; use an HTTP app cron when the schedule should make a request to an
app route. Use a command cron when a recurring task needs the app's deployment
environment without an HTTP endpoint:

```bash
gregale crons add --app APP_ID --schedule "0 2 * * *" \
  --command bin/rebuild-index --arg=--incremental --timezone Europe/Istanbul
gregale crons add --app APP_ID --schedule "0 4 * * 0" \
  --command "bin/cleanup --older-than 30d" --shell
gregale crons runs CRON_ID
```

Command crons create one deployment-attached app task for each scheduled
occurrence and select the app's currently live deployment at fire time, so a
later deployment automatically supplies the new command environment. The
command runs with a 10-minute timeout and 1 MiB output limit by default; use
`--timeout-seconds` and `--max-output-bytes` to adjust them. `--arg` is
repeatable and preserves argument boundaries. `--shell` instead treats the
single `--command` value as a shell string and cannot be combined with `--arg`.
Use `--skip-if-running` to skip a firing while an earlier command task remains
active. By default command failures are not retried. Set `--retry-max` to allow
up to five additional attempts after a command fails or times out; retries use
exponential backoff from `--retry-backoff-seconds` (default 60 seconds, capped
at 24 hours). A cron run remains one logical history row while it waits for a
retry, and its task receipt reports the attempt count and next retry time.
Retries are at-least-once: a command may have produced side effects before it
failed, so make retryable commands idempotent. A worker lease lost after
dispatch is not automatically replayed because completion is uncertain.

Inspect outcomes with `crons runs`; the returned `task_id` can be used
with `GET /v1/apps/APP_ID/tasks/TASK_ID` to read captured output. Command crons
do not support fire-now, while `gregale app APP_ID exec ...` remains the
one-off command surface. Scheduled jobs create one task per occurrence and
pick up the job's current configuration at fire time.

Handlers receive an event id and delivery attempt. Persist that id before applying side effects so retries are idempotent. Set explicit payload limits, timeouts, retry counts, and retention; route poison messages to a dead-letter destination for inspection and replay.

Keep synchronous endpoints small and return an acknowledgement only after the event is durably accepted. Verify webhook signatures and record the source, schema version, and correlation id in application logs.

Async invocations may name existing app webhook subscriptions as terminal
destinations. The success destination receives a durable `job.finished` event
after completion; the failure destination receives the same envelope when the
invocation permanently fails or exhausts its retry budget. Webhook delivery
has its own retry and dead-letter lifecycle, so a downstream outage does not
change the invocation result.

## Application inbox

For straightforward application-to-application work, send directly to the
target app without operating RabbitMQ, SQS, or NATS:

```bash
gregale send billing --type invoice.created \
  --data '{"invoice_id":"inv_123"}'
```

The equivalent API is `POST /v1/apps/billing/inbox`; the Go, Node, and Python
SDKs expose `SendAppMessage` / `sendAppMessage` / `send_app_message`.
Gregale normalizes the request into a CloudEvents 1.0 envelope and places that
envelope on the target's durable invocation queue. The normal queue depth,
wake, retry, trace, dead-letter, and replay behavior applies. Use `--id` for a
stable logical event id and `--idempotency-key` (or the API's
`Idempotency-Key` header) when retrying an uncertain request.

Delivery is at least once. The receiver must deduplicate the envelope's `id`
before applying non-idempotent side effects. This is an application inbox for
ordinary API-to-worker or service-to-service work, not a partitioned streaming
log or a Kafka replacement.

## Application outbox

Register the customer endpoint once so Gregale can validate the URL and seal
its signing secret, then deliver arbitrary application events through it:

```bash
gregale webhooks add --app checkout-api \
  --target-url https://customer.example/webhook \
  --secret "$WEBHOOK_SECRET"

gregale deliver checkout-api https://customer.example/webhook \
  --type order.paid --data '{"order_id":"ord_123"}'
```

The destination may also be the registered webhook id. The equivalent API is
`POST /v1/apps/checkout-api/outbox`; the first-party SDKs expose matching
delivery methods. Explicit delivery uses the registered destination's HMAC
secret, delivery format, timeout, and retry policy. It appears in the existing
delivery history and unified DLQ, where dead deliveries can be inspected and
replayed. The subscription's platform-event filter does not block an explicit
delivery to that destination.

Gregale signs the same canonical string and emits the same delivery headers as
ordinary app webhooks. Receivers should verify the signature, reject stale
timestamps, and deduplicate the durable delivery id.
Use `--idempotency-key` when retrying a `gregale deliver` call whose outcome
is unknown; a new key can enqueue a second delivery.

## Deployment lifecycle webhooks

Subscribe to `deployment.live` and `deployment.failed` to drive a platform's
customer-facing deployment status without polling:

```bash
gregale webhooks add --app checkout-api \
  --target-url https://platform.example/deployments \
  --secret "$WEBHOOK_SECRET" \
  --event deployment.live --event deployment.failed
```

An event is enqueued when the deployment's durable status *changes* to `live`
or `failed`. Rewriting the same status does not enqueue it again; creating a
subscription does not replay old transitions. The payload identifies the app,
deployment, and status. Failures also include any available `error_code`,
`error_hint`, `error_why`, and `error_fix`; internal error text and log excerpts
are not sent. `deployment.live` means the deployment reached the live state,
not that a progressive rollout reached 100% traffic.

Delivery is at least once. Verify the webhook signature and deduplicate by the
durable delivery id in the webhook envelope or headers; retries keep that id.
The existing delivery history and dead-letter retry API cover these events.

## Rollout outcome webhooks

Subscribe to `rollout.completed` and `rollout.aborted` when an external
platform needs to close a release workflow after the deployment becomes live:

```bash
gregale webhooks add --app checkout-api \
  --target-url https://platform.example/rollouts \
  --secret "$WEBHOOK_SECRET" \
  --event rollout.completed --event rollout.aborted
```

`deployment.live` means the revision is ready to serve. It does not mean its
canary has finished. `rollout.completed` means the configured rollout entered
the `complete` state; the payload includes `traffic_percent` because an
explicit traffic split can complete below 100%. `rollout.aborted` means an
in-progress live rollout entered `aborted`, with its customer-visible reason
and final traffic percentage. A build that fails before going live emits
`deployment.failed` instead of `rollout.aborted`.

Both payloads include `app_id`, `deployment_id`, and `rollout_state`, plus
`completed_at` or `aborted_at` respectively. An outcome is enqueued once per
state transition, in the same transaction as the state change; updating an
already-terminal rollout does not enqueue another event. Webhook delivery is
at least once, so receivers must deduplicate by the stable delivery id.

## Delayed tasks

Delayed tasks are durable one-shot invocations. The producer asks Gregale to
invoke an app at a future time, so it does not need to operate Redis, SQS,
EventBridge, or cron infrastructure.

Use either an absolute timestamp or a relative delay:

```bash
gregale delayed-task add --app billing --delay 30m \
  --path /internal/send-reminder --payload '{"invoice_id":"inv_123"}' \
  --header 'X-Correlation-ID:inv_123' \
  --max-attempts 4 --retry-base-seconds 1 --retry-max-seconds 30 \
  --retry-jitter-seconds 0.2 --retention 24h \
  --on-failure-webhook FAILURE_WEBHOOK_ID \
  --idempotency-key reminder-inv-123

gregale delayed-task add --app billing \
  --scheduled-at 2026-10-01T09:00:00Z --method POST --path /month-close
```

The API accepts the same target envelope as an asynchronous invocation:
`method`, `path`, `headers`, `payload`, `retry_policy`, `retention_seconds`,
and optional success/failure webhook destinations. Exactly one of
`scheduled_at` or `delay_seconds` is required. The time must be in the future
and no more than 365 days away.

The CLI exposes that complete envelope through repeatable `--header` flags,
the `--max-attempts` and `--retry-*` retry controls, `--retention`, and
`--on-success-webhook` / `--on-failure-webhook`. Retry settings must satisfy
the plan limits; omit them to use the plan defaults.

List, inspect, or cancel work without querying the general invocation ledger:

```bash
gregale delayed-task list --app billing
gregale delayed-task get TASK_ID
gregale delayed-task cancel TASK_ID
```

Cancellation prevents execution only while a task is still `pending`. A task
already `dispatching` may complete. Dispatch is at least once: retries after a
worker or network failure can invoke the target more than once, so application
side effects must be idempotent. Use a stable `Idempotency-Key` (or the CLI's
`--idempotency-key`) when retrying creation after an uncertain response; that
prevents duplicate task rows during the API's 24-hour replay window.

Paid plans support delayed tasks. The per-app pending limits are Hobby 5, Pro
50, and Scale 1,000,000. A purpose-built API key can use
`delayed_tasks:write` to create/cancel and `delayed_tasks:read` to list/get;
existing `deploy:write` and `apps:read` keys remain compatible.

## Internal event subscriptions

Applications can subscribe to events published through Gregale's internal
event router. A YAML deployment declares subscriptions with `event_triggers`:

For a first worker, scaffold the complete example instead of writing the
manifest and handler by hand:

```bash
gregale init --template event-worker --path ./invoice-worker
cd invoice-worker && gregale deploy --name invoice-worker
```

The generated project includes a safe `billing.*` / `invoice.paid` filter and
an acknowledgement handler. Edit `gregale.yaml` when your event vocabulary is
ready.

```yaml
event_triggers:
  - source: billing.*
    type: invoice.paid
    filter: '{"data":{"amount":{"$gt":100}}}'
```

The filter is a JSON object encoded as a string. `app` is optional for a
single-app deploy and is bound to the target application during source-ref
reconciliation. Event-only projects may use the equivalent TOML form,
`[[triggers.event]]`. Both forms use the same source/type/filter validation;
matching deliveries inherit the router's retry and dead-letter behavior.
When a delivery reaches a terminal failure, inspect and replay it through the
app's unified dead-letter queue (`gregale dlq <app>`); its origin is shown as
`event_subscription`.

Publish an event from the CLI without learning the CloudEvents envelope. Gregale
generates the stable event id for you:

```bash
gregale events publish billing.stripe invoice.paid \
  --data '{"amount":150}'
```

Use `--id` when a producer is retrying the same logical event and needs to
choose the idempotency key explicitly. The flag-based form remains supported:

```bash
gregale events publish --id evt-123 --source billing.stripe --type invoice.paid \
  --data '{"amount":150}'
```

Before publishing, check which enabled subscriptions would receive a sample.
Preview uses the router's matcher but does not persist the event or enqueue
invocations:

```bash
gregale events preview billing.stripe invoice.paid \
  --data '{"amount":150}'
```

The summary separates subscriptions that would receive the event from those
rejected by content filters. `--id` and `--time` can be supplied when a filter
inspects those CloudEvents attributes. `--json` returns the bounded samples and
complete match counts; previews require only the read API-key scope.

To confirm what Gregale reconciled for an app, list its active manifest
subscriptions directly:

```bash
gregale events subscriptions APP
gregale events subscriptions APP --json
gregale events deliveries APP
gregale events deliveries APP --state failed --json
```

This is useful after a deploy or manifest change: it shows the normalized
source, type, filter, and enabled state that the router will use.

After publishing, use `events deliveries` to see the matching event id,
delivery state, attempt count, and last lifecycle timestamp without searching
the account-wide invocation ledger. `--event-id` narrows the view to one
published event; `--state` can focus on pending, failed, or dead-lettered
deliveries.

The machine-readable event contract is published in
[`api/asyncapi.yaml`](../api/asyncapi.yaml), including the authenticated
`POST /v1/events:publish` ingress.
