# Event-driven workloads

Use asynchronous invokes, jobs, webhooks, and scheduled triggers when work does not need to finish in the request path.

## Durable workflow waits

A declarative workflow can pause between handler invocations without keeping a
VM alive. For example, this order flow charges a card, waits three fixed
24-hour days, then checks delivery:

```yaml
workflows:
  - name: order_followup
    trigger:
      type: manual
    steps:
      - name: charge
        run: charge_card
        retry:
          max_attempts: 3
          backoff: exponential
      - name: delivery_delay
        wait_for_duration: 3d
        depends_on: [charge]
      - name: check_delivery
        run: check_delivery
        depends_on: [delivery_delay]
      - name: send_email
        run: send_email
        depends_on: [check_delivery]
```

Handler retries persist a `next_retry_at` deadline per step, so an unrelated
event cannot run a retry before its configured backoff expires. When a workflow
has parallel waits and retries, the scheduler wakes it for the earliest due
step.

Deploy the manifest, then start a run with:

```bash
gregale workflows run order_followup --app APP_SLUG --input '{"order_id":"ord_123"}'
```

The timer
starts only when its dependencies succeed. It is stored in the workflow
ledger and resumed by the scheduler when due; no application instance is
reserved for the wait. `wait_for_duration` accepts `1s` through `7d` on
workflow-enabled plans. It cannot be combined with `run`, `path`,
`wait_for_event`, `timeout`, `on_timeout`, or `retry` in one step.

For a one-time callback, declare a separate `wait_for_callback: true` step
with a `timeout` and optionally an `on_timeout` handler:

```yaml
- name: await_delivery
  wait_for_callback: true
  timeout: 3d
  on_timeout: delivery_timeout
  depends_on: [charge]
- name: delivery_timeout
  run: notify_support
```

After starting the run, an account-authorized client calls
`GET /v1/workflows/runs/{id}/callbacks` to get the stable callback ID for
`await_delivery`. It completes the step with
`POST /v1/workflows/runs/{id}/callbacks/{callback_id}` and a JSON body.
Completion can arrive before the step activates. Repeating the same JSON
returns a duplicate receipt; sending a different body conflicts. The timeout
starts when the step first becomes runnable, not when the run was created.
The workflow stays parked between callbacks; no application instance is
reserved for the wait. The callback ID is not an authentication token:
both requests need the owning account's API authorization.

If an external system cannot push an event, use a bounded condition checker:

```yaml
- name: await_delivery
  wait_for_condition:
    run: check_delivery
    interval: 30m
    max_attempts: 100
  timeout: 3d
  on_timeout: notify_support
  depends_on: [charge]
```

`check_delivery` receives the workflow input on its first call. It must return
JSON with a boolean `done`, for example `{"done":false,"state":{"order_id":"ord_123"}}`.
The entire false result is sent as input to the next check; `done:true` makes
the result the step output and unlocks dependents. Gregale persists each result
and the next check time, then releases compute between calls. A 5xx or transport
error retries on the same schedule; malformed 2xx and 4xx responses fail the
run. The interval is at least one minute, with at most 1,000 checks and a
plan-bounded overall timeout (currently at most seven days). Attempt
exhaustion follows `on_timeout` when present. Checker calls are at least once;
use their stable `Idempotency-Key` for any side effects. A push event or
callback remains preferable when the external system supports one.

For an unsupported external provider, receive and verify its webhook in your own handler,
then use that authenticated Gregale API to complete the callback. Do not give
the provider your Gregale API key. There is no per-callback public URL.
For repeatable or broadcast signals, continue using `wait_for_event` and
`POST /v1/workflows/runs/{id}/events` with a stable `Idempotency-Key`.

Stripe can instead complete a callback without waking your app. Create a
[durable inbound Stripe webhook endpoint](inbound-webhooks.md) for the
same app and configure its one-time-disclosed URL in Stripe. Then bind the
known Stripe object to a callback:

```http
PUT /v1/workflows/runs/RUN_ID/callbacks/CALLBACK_ID/webhook-binding
Authorization: Bearer GREGALE_API_KEY
Content-Type: application/json

{"endpoint_id":"ENDPOINT_ID","event_type":"payment_intent.succeeded","object_id":"pi_123"}
```

The existing endpoint verifies Stripe's signature over the raw body. Only an
exact event-type and object-ID match consumes the callback; other events still
enter the app's ordinary durable webhook inbox. A matching verified event
completes the workflow callback directly and returns `202` after persistence.
Provider retries are deduplicated, and a late event for a closed callback is
acknowledged as ignored. Bind before the provider event arrives; an event
received earlier follows the normal app-delivery path. Use the binding's
`GET` and `DELETE` operations to inspect or revoke it. This first adapter
supports Stripe only; other providers still need a verifying application
handler.

This is a declarative workflow, not a replayed single function: handlers are
separate at-least-once invocations and must make external side effects
idempotent. Gregale does not yet provide `ctx.sleep()` or year-long
code-as-workflow executions. Its existing short-lived `ctx.waitUntil()`
post-response tail is separate from durable `wait_for_condition` checks.

```bash
gregale invoke --async --payload @payload.json APP_ID
gregale invoke --async --on-success-webhook WEBHOOK_ID --on-failure-webhook DLQ_WEBHOOK_ID APP_ID
gregale jobs run nightly --tasks 10
gregale crons add --app APP_ID --schedule "0 * * * *" --path /jobs/nightly
```

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
