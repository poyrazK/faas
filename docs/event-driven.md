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
        input:
          order_id: "{{input.order_id}}"
        retry:
          max_attempts: 3
          backoff: exponential
      - name: delivery_delay
        wait_for_duration: 3d
        depends_on: [charge]
      - name: check_delivery
        run: check_delivery
        depends_on: [delivery_delay, charge]
        input:
          order_id: "{{input.order_id}}"
          charge_id: "{{steps.charge.output.charge_id}}"
      - name: send_email
        run: send_email
        depends_on: [check_delivery]
        input:
          order_id: "{{input.order_id}}"
          delivery: "{{steps.check_delivery.output}}"
```

Each handler step can declare an `input` JSON template. `{{input}}` selects
the complete workflow input and `{{input.path.to.value}}` selects a field;
`{{steps.STEP.output}}` selects a dependency's full JSON result, with dotted
fields or numeric array indexes available after `output`. Step-output
references must name a direct `depends_on` step. A reference occupying a
whole JSON value preserves its type (including objects, arrays, numbers, and
booleans); a reference embedded in a longer string must resolve to a scalar.
Missing paths fail the step before the handler is invoked. If `input` is
omitted, the handler receives the full workflow input as before. Gregale stores
the resolved input before dispatch, so retries reuse the same payload even if
the scheduler restarts.

Executable steps receive an `Idempotency-Key` of
`workflow/<run-id>/<step-name>`, unchanged across automatic retries; the
separate `X-Faas-Workflow-Attempt` header increments. Deduplicate external
side effects on that key. Delivery is still at least once, not exactly once.

### Transactional HTTP workflow steps

Set `managed_operation: true` on an executable step to make retries replay the
same customer-database transaction result:

```yaml
- name: reserve
  run: reserve_order
  managed_operation: true
  retry:
    max_attempts: 3
    backoff: exponential
```

The handler must use the managed-operation transaction wrapper described in
[`operation-transactions.md`](operation-transactions.md). Gregale assigns one
stable operation ID to the run and step, and advances the operation generation
with each workflow attempt. The wrapper commits the business writes and saved
result together. If Gregale retries after losing the HTTP response, the wrapper
replays the saved result without repeating those writes. The workflow stores
the result value as step output, so dependent steps receive the business result
rather than the protocol envelope. The resolved step input is also persisted
before dispatch, keeping the request fingerprint stable across retries.

The wrapper may also return named webhook effects. Before the step is marked
succeeded, Gregale verifies that each `webhook_id` is an enabled webhook owned
by the workflow app and explicitly subscribed to `operation.effect`. It then
records the effect, queues its signed delivery, and completes the step in one
platform transaction. Delivery uses the ordinary at-least-once webhook
dispatcher. Register the receiver under `POST /v1/apps/{slug}/webhooks`; tenant
receivers are not supported for workflow effects. See
[`managed-operation-effects.md`](managed-operation-effects.md) for the handler
envelope and event payload.

The customer database commit and Gregale's result/effect transaction are
separate. If a receiver is disabled or invalid when Gregale accepts the result,
the workflow step fails after the business transaction has committed. Configure
the receiver before dispatch and make business writes safe to reconcile by the
stable operation ID. Inspect delivery status with
`gregale workflows attempts <run_id> <step_name>`; each attempt includes its
effect and delivery IDs, status, retries, and last error.

### Recover from a failed step

Use `on_failure` to run a compensating or notification handler after a step has
exhausted its retries (or its input template cannot be resolved):

```yaml
- name: charge
  run: charge_card
  retry:
    max_attempts: 3
    backoff: exponential
  on_failure: refund
- name: refund
  run: refund_order
  depends_on: [charge]
  input:
    order_id: "{{input.order_id}}"
    failure: "{{failure}}"
```

The handler is skipped when `charge` succeeds. Its default input, when `input`
is omitted, is an envelope containing the original run input and failure
context: `{"input": ..., "failure": ...}`. Templates can select
`{{failure.step}}`, `{{failure.status}}`, `{{failure.attempt}}`, and
`{{failure.message}}`; the whole `{{failure}}` reference preserves the object.
Failure context is available only to the step named by `on_failure`.

This is a recovery hook, not error suppression: once the handler finishes, the
workflow run still ends failed or dead. The original error remains the run's
primary error; a handler's own failure is recorded on that handler step. Each
executable step can route to one distinct executable handler, and handlers
cannot chain `on_failure` or have downstream workflow steps.

Handler retries persist a `next_retry_at` deadline per step, so an unrelated
event cannot run a retry before its configured backoff expires. When a workflow
has parallel waits and retries, the scheduler wakes it for the earliest due
step.

Deploy the manifest, then start a run with:

```bash
gregale workflows run order_followup --app APP_SLUG --input '{"order_id":"ord_123"}'
```

Inspect the latest step summary with `gregale workflows steps RUN_ID`. To see
each handler invocation or condition-check poll—including its outcome, HTTP
status, start/finish times, error, and next scheduled attempt—use:

```bash
gregale workflows attempts RUN_ID STEP_NAME
```

The equivalent read-only API is
`GET /v1/workflows/runs/{id}/steps/{step}/attempts`. Attempt history stores
metadata, not request or response bodies; timer and callback waits do not
create executor-attempt records.

For a terminal run with one failed or dead HTTP step, retry that step in place:

```bash
gregale workflows retry RUN_ID STEP_NAME
```

This preserves the run ID, definition snapshot, resolved input, and existing
attempt history. Gregale appends a new attempt and reopens skipped
`depends_on` descendants so the scheduler can continue the DAG. The retry is
rejected if another step is active, failed, or dead; a downstream step already
succeeded; the target is a wait or failure-handler step; or the run was
cancelled. A managed-operation handler keeps the same run/step operation ID,
so its transaction SDK can replay a receipt already committed before a lost
response. Each manual retry grants one new dispatch and does not reset the
manifest's automatic retry budget; after another terminal failure, you can
request another manual attempt. Ordinary HTTP handlers still need their own
idempotency because delivery remains at least once. The API is
`POST /v1/workflows/runs/{id}/steps/{step}/retry`.

The timer
starts only when its dependencies succeed. It is stored in the workflow
ledger and resumed by the scheduler when due; no application instance is
reserved for the wait. `wait_for_duration` accepts `1s` through `30d` on
Hobby, `90d` on Pro, or `365d` on Scale. `wait_for_event` and
`wait_for_callback` timeouts use the same per-plan horizon. A timer cannot be
combined with `run`, `path`,
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
separate seven-day maximum interval and overall timeout, even on plans that
allow longer parked waits. Attempt exhaustion follows `on_timeout` when
present. Checker calls are at least once and get a per-check
`Idempotency-Key` (`workflow/<run-id>/<step-name>/<attempt>`), so distinct
polls remain distinct. A push event or
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
Failure rules can classify an exit code or a structured `outcome_code` written
to `GREGALE_OUTPUT_MANIFEST_PATH`; a mapped code can turn exit 0 into a
permanent failure or a retry. Unmapped successful outcomes remain successful.
Retries are at-least-once: a command may have produced side effects before it
failed, so make retryable commands idempotent. A worker lease lost after
dispatch is not automatically replayed because completion is uncertain.

To report a business outcome independently of the process exit status, write
a version-1 result manifest atomically before the command exits:

```bash
printf '%s\n' '{"version":1,"artifacts":[],"outcome_code":"invalid_record"}' \
  > "$GREGALE_OUTPUT_MANIFEST_PATH.tmp"
mv "$GREGALE_OUTPUT_MANIFEST_PATH.tmp" "$GREGALE_OUTPUT_MANIFEST_PATH"
```

Configure that code explicitly with `--failure-rules`; Gregale does not infer
retry safety from an error message. The command receipt retains the code and
classifier decision for inspection.

Inspect outcomes with `gregale crons runs CRON_ID`. The history includes a run
id; for a command cron, inspect its captured stdout/stderr, exit status, and
retry details on demand with `gregale crons runs CRON_ID --run TASK_ID`. Use
`gregale crons run CRON_ID` to immediately run the cron's saved command on the
current live deployment without moving its schedule cursor.
If `--skip-if-running` is configured and another run is still active, the
manual request fails rather than overlapping it.
Cancel a queued or active command-cron run with
`gregale crons cancel CRON_ID TASK_ID`; an active task reports its cancellation
request while the worker stops it. Disabling a cron only prevents future fires.
`gregale app APP_ID exec ...` remains the surface for an arbitrary one-off
command. Scheduled jobs create one task per occurrence and
pick up the job's current configuration at fire time.

Handlers receive an event id and delivery attempt. Persist that id before applying side effects so retries are idempotent. Set explicit payload limits, timeouts, retry counts, and retention; route poison messages to a dead-letter destination for inspection and replay.

Keep synchronous endpoints small and return an acknowledgement only after the event is durably accepted. Verify webhook signatures and record the source, schema version, and correlation id in application logs.

Async invocations may name existing app webhook subscriptions as terminal
destinations. The success destination receives a durable `job.finished` event
after completion; the failure destination receives the same envelope when the
invocation permanently fails or exhausts its retry budget. Webhook delivery
has its own retry and dead-letter lifecycle, so a downstream outage does not
change the invocation result.

Async edge rules can also set their own retry curve and maximum invocation
age instead of inheriting the app retry curve and plan deadline:

```json
{
  "retry_policy": {
    "max_attempts": 4,
    "base_seconds": 1,
    "max_seconds": 30,
    "jitter_seconds": 0.2
  },
  "max_age_seconds": 600,
  "on_failure": "WEBHOOK_ID"
}
```

`max_attempts` includes the initial attempt; the current plan caps the retry
budget. `max_age_seconds` starts when the edge accepts the request and is
clamped to the plan's maximum invocation deadline. Omit either setting (or
use zero for maximum age) to keep the existing app/plan default. The CLI
equivalents are `--async-max-attempts`, `--async-retry-base-seconds`,
`--async-retry-max-seconds`, `--async-retry-jitter-seconds`, and
`--async-max-age-seconds` on `gregale edge-rules create`.

For deployed apps, the same async route can be declared in `gregale.yaml` and
reconciled with the source deployment:

```yaml
async_routes:
  - app: reports
    name: create-report
    match_host: reports.example.com
    match_path: /reports
    match_methods: [POST]
    on_success: WEBHOOK_ID
    on_failure: DLQ_WEBHOOK_ID
    retry_policy:
      max_attempts: 4
      base_seconds: 1
      max_seconds: 30
      jitter_seconds: 0.2
    max_age_seconds: 600
```

`app` is the target app slug (or, in a project deploy, its workload name), and
destinations are existing webhook IDs from
`gregale webhooks list --app reports`. Route names are stable per app: later
deploys update a matching manifest-owned route and remove stale manifest-owned
routes. Unmanaged edge rules are never adopted or deleted; an exact route
collision fails deployment with guidance to resolve it first. Omitting
`async_routes` leaves managed routes unchanged, while `async_routes: []`
clears them. In project deploys, only selected workloads are reconciled;
omitted route declarations clear that workload's manifest-owned routes, while
`--only` and `--exclude` workloads remain untouched. `--no-triggers` leaves
existing project routes unchanged. Routes default to `POST`; `PUT`, `PATCH`,
and `DELETE` are also accepted. This declaration is YAML-only.

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
For an app without an active named queue consumer, `--work-policy` and
`--work-key` (plus optional `--work-fairness-key`) apply an application work
policy to the message. The API equivalent is the `work` field. These messages
use the keyed invocation dispatcher; see [Application work policies](work-policies.md).

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

Route-level regression notifications are documented in
[Route regression webhooks](route-regression-webhooks.md).

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

To coordinate related event deliveries, declare a named work policy and bind
the subscription to a scalar field in the CloudEvents envelope:

```yaml
work_policies:
  - name: document-index
    max_running_per_key: 1
    pending_updates: keep_latest
    debounce_ms: 3000
    expires_after_ms: 600000
event_triggers:
  - source: documents
    type: document.edited
    work_policy: document-index
    work_key: data.document_id
```

The same policy name and key can be supplied to an explicit async invocation.
Gregale serializes dispatch within that lane and can replace older pending
work. Running work continues; handlers should still protect external side
effects with an idempotency key or version check. See
[application work policies](work-policies.md) for the complete contract.

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

Gregale identifies an event by account, source, and id. Repeating that
identity with the same type, schema version, and JSON data is safe. Changing
the content returns `409 Conflict` within the 30-day identity retention
window. Fanout work is stored with the event, so delivery resumes after a
scheduler outage regardless of its duration or backlog size. New events capture
their enabled source/type subscription candidates when they are accepted. A
later subscription edit or deletion does not change that event's recipients;
the captured JSON data filter is evaluated when fanout runs. Receipts accepted
before the recipient-snapshot migration continue using the previous routing
behavior, which reads current subscriptions. The scheduler records a routing
outcome for each captured candidate, so a transient enqueue error retries only
that candidate. Retries are capped at 12; terminal routing failures remain on
the outbox receipt and are logged by the scheduler. Once an invocation is
enqueued, its handler retry and dead-letter lifecycle applies independently.
Published and inbox envelopes use CloudEvents `datacontenttype` and the
`accountid` extension. The API accepts the older `data_content_type` and
`account_id` request spellings for existing clients.
Sources must be URI references, such as `urn:example:billing`,
`https://example.com/events`, or `billing.service`; percent-encode spaces.
CloudEvents webhook delivery also uses `accountid`. Receivers of the opt-in
CloudEvents format must read that extension instead of the previous invalid
`account_id` extension; the legacy Gregale JSON format keeps its existing shape.


An API key with `events:publish` can publish and send to an app inbox;
`queues:send` permits queue sends. Existing `deploy:write` keys continue to
work on these routes.

For a versioned event contract, register an immutable Draft 2020-12 JSON
Schema with `POST /v1/event-schemas` using a deploy key:

```json
{
  "source": "billing.stripe",
  "type": "invoice.paid",
  "version": "v1",
  "schema": {
    "type": "object",
    "required": ["amount"],
    "properties": {"amount": {"type": "number"}}
  }
}
```

Once the first version exists, producers must include
`"schemaversion":"v1"` in each matching publish. Gregale validates the
event data before accepting it; an unknown version or invalid payload
returns `422`. Schema versions cannot be changed in place. Use
`GET /v1/event-schemas?source=billing.stripe&type=invoice.paid` to inspect
registered versions. Sources beginning with `gregale.` are reserved for
platform events. Schemas cannot fetch external references.

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
the account-wide invocation ledger. The history includes operator replays and
labels each row's invocation source so original deliveries and replays are
easy to distinguish. `--event-id` narrows the view by event ID;
add `--event-source` to select one exact event identity when IDs are reused by
different sources. `--state` can focus on pending, failed, or dead-lettered
deliveries.

The same command also reports terminal recipient routing failures that
occurred before Gregale created an invocation. These rows include the event,
subscription, routing attempt count, stable `failure_code`, `retryable` signal,
failure time, and error. `invalid_subscription` and `target_unavailable` point
to configuration or target state that should be fixed before replay;
`target_lookup_failed` and `invocation_enqueue_failed` identify transient errors
that may succeed on replay. Older failures recorded before classification was
available use `unknown`. Use `--fanout-before` with `next_fanout_before` from
`--json` to page through that failure history independently of invocation
deliveries.

The latest routing checkpoint is useful for finding current failures, but an
operator replay changes that checkpoint. To inspect the immutable sequence of
routing outcomes and replay requests for one published event, use
`events fanout-history`:

```bash
gregale events fanout-history APP \
  --event-source billing.stripe \
  --event-id evt-123
```

History is ordered newest first and retained for the same period as the event
fanout receipt. Use `--subscription-id` to narrow it to one captured recipient;
use `--before` with `next_before` from JSON output to page through older rows.
Replay rows include the failure details that led to the replay, even after the
recipient later succeeds.

To retry one terminal pre-invocation failure, pass its event ID, source, and
subscription ID from the failure row:

```bash
gregale events replay APP \
  --event-id evt-123 \
  --event-source billing.stripe \
  --subscription-id 5ef2a270-2c12-4ddd-a2a7-a0873995f7c8
```

Replay becomes available after the event's fanout receipt settles. It queues
only that recipient and keeps the event payload and recipient configuration
captured when the event was accepted. A replay therefore uses the same filter
and target app; fix persistent routing or app problems before retrying. Other
recipients that already succeeded or failed are not rerun.

After a transient outage, replay retryable failures for an app in bounded
batches:

```bash
gregale events replay-retryable APP --limit 100 --yes
```

This command requeues only terminal failures classified as retryable, oldest
first, and never more than 100 recipients per call. Add both `--event-source`
and `--event-id` to scope it to one published event. `--yes` confirms the
batch; repeat the command when the response reports `has_more: true`. A queued
event can accept additional replay batches while pending. If its worker is
already processing it, the command leaves that event alone and continues to
report more failures; repeat after the event settles.
Configuration failures such as an invalid
subscription or unavailable target remain untouched for explicit repair and
single-recipient replay.

The machine-readable event contract is published in
[`api/asyncapi.yaml`](../api/asyncapi.yaml), including the authenticated
`POST /v1/events:publish` ingress.
