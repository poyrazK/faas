# Event-driven workloads

Use asynchronous invokes, jobs, webhooks, and scheduled triggers when work does not need to finish in the request path.

## Start a workflow on a schedule

On Hobby and higher plans, add a scheduled trigger to a workflow in
`gregale.yaml` or its JSON equivalent:

```yaml
workflows:
  - name: daily_report
    trigger:
      type: schedule
      schedule: "0 7 * * *"
      timezone: Europe/Istanbul
      input:
        report: daily
      overlap: skip
      enabled: true
      tenant_configurable: true # optional: let each linked customer adjust this schedule
    steps:
      - name: generate
        run: generate_report
        retry:
          max_attempts: 3
          backoff: exponential
      - name: send
        run: send_report
        depends_on: [generate]
        input:
          report_id: "{{steps.generate.output.report_id}}"
```

Deploy the manifest with your application handlers. Gregale starts the workflow
directly; an HTTP cron-to-workflow adapter is unnecessary. `run: generate_report`
invokes the app's `/generate_report` handler via the existing workflow executor.
The [example manifest](../examples/scheduled-workflows/gregale.yaml) provides
this two-step recipe.

Workflow execution remains a preview feature. Operators must configure the
workflow executor and enable `FAAS_WORKFLOWS_ENABLED=1` on both `apid` and
`schedd`. Inspect the configuration and latest scheduling outcome with:

```bash
gregale workflows schedules --app APP_SLUG
gregale --json workflows schedules --app APP_SLUG
gregale workflows list --app APP_SLUG
gregale workflows steps RUN_ID
gregale workflows attempts RUN_ID STEP_NAME
```

The corresponding inspection API is
`GET /v1/apps/{slug}/workflows/schedules`. Its JSON envelope contains
`runtime_enabled` and `schedules`; `unavailable_reason` explains a blocking
runtime, account, plan, maintenance, or tenant requirement. `enabled` describes
the trigger configuration. `next_fire_at` is a nominal calendar time and is
omitted for disabled or unavailable schedules. Runtime availability reflects
apid's configuration; it does not prove that schedd is running. `last_status`
records `armed`, `started`, `skipped_overlap`, or `skipped_quota`;
`last_scheduled_for` and `last_run_id` connect an admission to its execution.
This endpoint keeps the latest outcome, not every skipped occurrence.

Schedules use the same five-field cron grammar and daylight-saving behavior
as application crons. UTC is the default timezone. Fixed `input` may be any
JSON value within the ordinary 1 MiB run-input limit and defaults to `{}`.
`overlap` defaults to `skip`: a pending, running, or waiting run of the same
workflow, including a manual start, skips that minute. `allow` permits overlap
within the app's existing concurrent-run quota. Quota refusal also skips the
minute. A skipped occurrence is consumed; freeing capacity later does not
retry it. You can still start the workflow manually with
`gregale workflows run daily_report --app APP_SLUG --input '{"report":"daily"}'`.

Only the live default deployment schedules work. Preview deployments do not
create background copies. A new or redeployed schedule first arms when the
scheduler observes it, then fires at its next eligible minute. Missed minutes
during downtime are discarded; recovery does not enqueue a backlog. Set
`enabled: false` and redeploy to stop app-owned scheduled starts. Existing runs
continue. Account suspension, abuse holds, Free-plan downgrade, and maintenance
mode block admission. Tenant-required apps start one run for each active linked
tenant; their cursors and overlap checks are independent, while the app-wide
workflow concurrency quota remains shared.

An app owner can add `tenant_configurable: true` to a schedule trigger to let a
linked customer manage its own cron expression, timezone, overlap behavior, and
enabled state. The customer token needs `platform_tenant:automations:read` to
list opted-in schedules and `platform_tenant:automations:manage` to update one:

```http
GET /v1/platform-tenant-self/apps/{slug}/workflows/schedules
Authorization: Bearer <tenant-token>

PUT /v1/platform-tenant-self/apps/{slug}/workflows/schedules/daily_report
Authorization: Bearer <tenant-token>
Content-Type: application/json

{"expected_version":0,"schedule":"0 6 * * 1-5","timezone":"Europe/Istanbul","overlap":"skip"}
```

Use the returned `version` as `expected_version` on each update; zero creates
the tenant's first override and stale versions return 409. Customers cannot
change workflow steps, credentials, the app-owned fixed input, or shared app
quota. Without the owner's opt-in, the published cadence remains in control.
Schedule definitions use the existing workflow-definition quota rather than
the separate HTTP/command-cron quota.

Workflow steps retain their existing at-least-once execution contract. Use the
stable workflow idempotency header for external effects even though duplicate
scheduler observations cannot create multiple runs for one consumed minute.

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
succeeded, Gregale verifies that each `webhook_id` is enabled and explicitly
subscribed to `operation.effect`, then records the effect, queues its signed
delivery, and completes the step in one platform transaction. Account-scoped
runs target an app receiver under `POST /v1/apps/{slug}/webhooks`. Tenant-bound
runs target a receiver owned by that same tenant under
`POST /v1/platform-tenants/{tenant_id}/webhooks`; the active tenant-to-app link
is checked when the result is committed and before each delivery attempt.
Delivery uses the ordinary at-least-once webhook dispatcher. See
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

## Event workflow starts

A workflow can start directly from an internal event without an adapter handler:

```yaml
workflows:
  - name: paid_invoice
    trigger:
      type: event
      source: billing.*
      event_type: invoice.paid
      filter:
        data:
          amount:
            $gt: 100
      enabled: true
    steps:
      - name: record
        path: /record-payment
        input:
          invoice_id: "{{input.data.invoice_id}}"
      - name: notify
        path: /send-receipt
        depends_on: [record]
        input:
          receipt_id: "{{steps.record.output.receipt_id}}"
```

The required source and event type accept the same exact and edge wildcard
patterns as subscriptions. The optional filter is a YAML/JSON object evaluated
against the CloudEvents envelope. A matching run receives that full envelope
as input, so `{{input.data.invoice_id}}` selects the producer's invoice ID.
Event triggers reject schedule, timezone, fixed input, and overlap fields.
Several events can start concurrent runs, subject to the existing app run quota.

Use `gregale events preview` before publishing; its samples include the workflow
name, deployment ID, and stable recipient ID. Preview checks matching intent and
does not reserve capacity or guarantee runtime availability. Publish with a
stable source and `--id` when retrying the same logical event. Find admitted runs
through `gregale workflows list --app APP`, then use workflow status, attempt,
and cancellation commands as usual. Ordinary subscription invocations remain
visible through `gregale events deliveries APP`.

Event starts are part of the workflow preview on Hobby and above. Enable
`FAAS_WORKFLOWS_ENABLED=1` on apid and schedd and configure the gateway executor.
Only the preferred live default deployment contributes workflow candidates;
preview deployments, maintenance apps, inactive or held accounts, and apps
requiring platform tenant context do not capture new recipients. Apply the
migration and upgrade schedulers before deploying these manifests. Older
schedulers cannot interpret workflow recipients safely. Drain accepted workflow
receipts and finish or cancel their runs before rolling back binaries.

At publish time Gregale captures matching source/type candidates with their
workflow definitions and filters. Redeploying, disabling, or removing a trigger
changes future events; already accepted events keep the original definition.
Step handlers run against the app's serving deployment. No historical events
are backfilled. Identical event identities retain the original recipients.
Admission commits a run with a durable event receipt, preventing duplicate runs
on recovery even after run history is pruned. Event identities and receipts use
the existing 30-day retention window.

Independent recipient routing also covers workflow-only and mixed
workflow/application events. Workflow admission and its routing checkpoint
commit together under the recipient's lease. A blocked workflow does not make
a failed sibling wait for the parent receipt to settle before selective recovery.

Capacity or temporary target failures retry through the existing fanout system.
After the 12-attempt cap, inspect routing failures and replay them using
`gregale events deliveries APP` and `gregale events replay`; `subscription_id`
also identifies workflow recipients. You can select the same routing recovery
with `gregale events recover --source SOURCE --id EVENT_ID --subscription ID`
and preview it with `--dry-run`. After admission, inspect and recover execution
through the workflow run and step commands. Disabling the workflow runtime
leaves workflow recipients waiting without consuming new retry attempts. App
handler side effects must remain idempotent because workflow steps may retry.

See the [two-step event workflow recipe](../examples/event-workflows/README.md)
and [ADR-432](adr/432-event-workflow-starts.md). Independent workflow admission
and its upgrade requirements are recorded in
[ADR-648](adr/648-independent-workflow-event-routing.md).

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

To preserve event acceptance order for one key, opt the subscription into
ordered delivery and use a policy that keeps every item until it runs:

```yaml
work_policies:
  - name: ordered-orders
    max_running_per_key: 1
    pending_updates: all
event_triggers:
  - source: orders
    type: order.changed
    work_policy: ordered-orders
    work_key: data.order_id
    ordered: true
```

For an opted-in subscription, Gregale routes the oldest unresolved matching
delivery first for each app, policy, and canonical scalar key. A routing retry
holds that key until it succeeds or reaches a terminal failure; different keys
can proceed independently. After routing admission, the work policy keeps
invocations for that key serialized and in the admitted order. Ordered policies
must set `max_running_per_key: 1`, `pending_updates: all`, and leave debounce
and expiry at zero. These restrictions prevent coalescing, expiry, and parallel
execution from violating the guarantee.

Manual replay restores routing for a failed delivery, but cannot undo a younger
invocation that was already admitted or completed. Replaying older events after
newer work has advanced can therefore change the observed order. Ordering is
per keyed lane; Gregale does not impose a global order across keys or unrelated
subscriptions.

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

### Retention health and expiry alerts

Inspect retained receipts and storage pressure before scheduling recovery:

```sh
gregale events retention --window 24h --json
gregale events retention --source orders --app order-worker --window 6h --limit 20
```

`GET /v1/events/retention` accepts the same `source`, `app`, `window` and `limit`
query parameters under account read scopes and MFA. The lookahead is a whole-second
Go duration from `1s` through `720h`, default `24h`; samples are capped at 100.
Aggregate counts include all matching receipts. Samples contain identities and
nominal deadlines, ordered by deadline, source and id, with `sample_truncated`
when more matches exist. Payloads and work keys are not returned.

Receipt retention begins when routing settles, not when the producer accepts an
event or a handler finishes. The nominal boundary is settlement plus 30 days.
Unsettled receipts have no pruning deadline. Settled receipts missing a settlement
timestamp are counted as `unknown_deadline_receipts`; expiry alerts degrade
rather than treating these as healthy zero. Hold counts describe settled receipts.
`eligible_for_pruning` counts
unheld receipts strictly past that boundary. `expiring_receipts` counts unheld
receipts from the observation instant through the lookahead, inclusive.
`held_receipts` and `held_due_receipts` distinguish backfill and recovery holds from upcoming
expiry and overdue cleanup. Holds use the exact pruning predicate: running
backfills pin their account acceptance ranges, and retained completed backfills
with retryable failed items pin those receipts. Running holds take precedence
when a receipt has both reasons; backfill holds take precedence over recovery holds.
`recovery_holds` counts settled receipts primarily held by opted-in pending recovery
items. The three hold counts partition `held_receipts`, counting receipts, not jobs.

Bulk recovery jobs protect pending receipts only when created with
`protect_receipts: true` (`--protect-receipts`). Current holds are observations;
other jobs and backfills can release them before a later recovery. Pruning eligibility does
not mean immediate deletion: batching and locks can delay cleanup. No read
extends retention or triggers pruning or recovery.

Source and app filters apply to receipt counts and samples. App attribution uses
captured or backfilled recipient membership; older receipts without that
attribution remain visible in the account-wide report. The `storage` object and
all utilization percentages always cover the entire account. Maximum utilization
is the larger of count and byte utilization; a plan downgrade can put it above
100%. Filtered receipt bytes can differ from account usage, and platform receipts
with no customer storage charge can still appear in receipt counts.

Create webhook-only alerts through existing app alert rules:

- `event_retention_expiring_receipts` counts unheld app receipts already eligible
  for pruning or expiring within the rule's `5m`, `15m`, `1h`, `6h` or `24h` lookahead.
- `event_storage_utilization_pct` observes the account-wide maximum count/byte
  utilization; use a threshold such as 90 to warn before storage fills.

These metrics require an owned app, omit `event_subscription_id`, and use current
observations rather than historical aggregation. Storage rules attached to different
apps observe the same account pressure. Existing cooldown and recovery webhook
notifications apply; no rules are installed automatically. Read failures produce
degraded observations rather than healthy zeros, and deployment actions are prohibited.

`events recovery-preflight` also reports pending items whose unheld receipt
boundary falls before the later of 24 hours from observation and optimistic drain.
It includes current hold counts, the earliest unheld boundary and a flag when the
rate-only minimum drain reaches or crosses it. Counts refer to pending items,
which may share a receipt. Missing receipts retain their existing classification.
A false crossing flag does not guarantee protection: future waits and changing
holds can still cause receipts to be pruned before recovery.

Apply the retention-health migration before upgrading binaries. Remove the new
alert rules and downgrade binaries before rolling it back. See
[ADR-830](adr/830-event-retention-health.md).

### Batch event publishing

Use `POST /v1/events:publish-batch` to publish 1–100 events in one request
of at most 1 MiB. Send `{"events":[...envelopes...]}` using the same event
attributes, account authentication, MFA and scopes as single-event publication.
Every event must have a stable caller-chosen `id`.

For JSONL imports, put one envelope on each line:

```jsonl
{"id":"order-101","source":"orders","type":"order.created","data":{"order_id":"101"}}
{"id":"order-102","source":"orders","type":"order.created","data":{"order_id":"102"}}
```

```sh
gregale events publish-batch --file orders.jsonl --json
# Or: gregale events publish-batch --file - < orders.jsonl
```

The CLI sends one bounded batch, requires stable ids, and exits nonzero if any
item is rejected or has an unknown outcome. It prints all results before exiting.
Blank JSONL lines are ignored; result indexes refer to event positions, not file
line numbers. It rejects malformed input locally before sending anything.

A structurally valid batch returns HTTP 200 with `results` in input order. Each
result has a zero-based `index`, `status` and `retryable`:

| Status | Meaning | Next action |
| --- | --- | --- |
| `accepted` | New event durably accepted; includes `receipt`. | Inspect the receipt for asynchronous delivery. |
| `duplicate` | Identical identity already accepted; includes the original `receipt`. | No additional fanout or storage charge. |
| `rejected` | Item not accepted; includes `problem`. | Correct invalid content, or retry if `retryable` is true. |
| `unknown` | Acceptance could not be confirmed; includes `problem`. | Retry with exactly the same identity and content. |

Each item commits independently. Invalid attributes, identity conflicts, schema
failures or storage capacity do not roll back accepted siblings. Capacity
problems include the existing limit, observed and retry-after fields. Processing
has a 30-second budget; remaining unattempted items are retryable rejections
with code `event_publish_not_attempted`. Malformed outer JSON, invalid batch
counts and oversized bodies reject the whole request before writes (400 or 413).
Authentication and request rate limiting still apply to the whole request.

Retry an unanswered request, the original file, or only retryable items using
the original source/id/content. There is no request-wide Idempotency-Key replay;
per-event identity supplies deduplication within the existing 30-day retention
window. Do not generate new ids when retrying. A 200 response does not mean
all events were accepted, and an acceptance receipt does not mean delivery or
handler execution succeeded.

Newly accepted items follow input acceptance order. Duplicates keep their
original position, rejections have none, and concurrent requests may interleave.
Execution ordering still requires opted-in keyed delivery; there is no global
ordering guarantee. Delivery remains at least once. See
[ADR-829](adr/829-batch-event-publication.md).

Gregale identifies an event by account, source, and id. Repeating that
identity with the same type, schema version, and JSON data is safe. Changing
the content returns `409 Conflict` within the 30-day identity retention
window. Fanout work is stored with the event, so durable routing resumes after a
scheduler outage. Configured delivery age limits can expire stale recipients. New events capture
their enabled source/type subscription candidates when they are accepted. A
later subscription edit or deletion does not change that event's recipients;
the captured JSON data filter is evaluated when fanout runs. Receipts accepted
before the recipient-snapshot migration continue using the previous routing
behavior, which reads current subscriptions. The scheduler records a routing
outcome for each captured candidate, so a transient enqueue error retries only
that candidate. Retries are capped at 12; terminal routing failures remain on
the outbox receipt and are logged by the scheduler. Once an invocation is
enqueued, its handler retry and dead-letter lifecycle applies independently.

Snapshot-backed application routing admits the invocation or work-policy
cancellation and records success in one transaction. An expired routing worker
cannot enqueue, supersede or cancel work. Recovery after an uncertain commit
uses the stored checkpoint, including after delivery rows are pruned; it cannot
admit another delivery or cancel work created later while the receipt is retained.
This applies to both whole-event and independent-recipient routing once every
scheduler is upgraded. Older receipts without snapshots and specialized object
notification destinations keep their existing routing paths. See
[ADR-613](adr/613-atomic-event-routing-handoff.md) for the transaction and upgrade
boundary. Handler side effects still require application deduplication.

Independent recipient routing is enabled by default (ADR-647). Each captured
candidate has its own five-minute lease and backoff from five seconds to five
minutes. A terminal recipient can be replayed while its siblings are routing
or waiting to retry. Each replay gets a fresh twelve-attempt routing budget;
the visible attempt count and history remain cumulative. Successful siblings
are not rerun. On schedd, an unset `FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED` or the
value `1` enables adoption. Set it to `0` on every scheduler before a
mixed-version upgrade; enable adoption after the migrations are applied and
all API and scheduler writers support recipient ownership and atomic admission.
Other nonempty values also disable adoption. Disabling adoption continues
draining already adopted receipts and does not make old binaries safe to restore.
Captured workflows use independent recipient routing too. Upgrade all
schedulers to support atomic workflow recipient admission before enabling
adoption during a mixed-version upgrade. Receipts without snapshots keep
legacy routing. Explicitly disabled adoption keeps whole-event ownership for
new receipts, including those with workflows.
Subscription backfill jobs keep their existing recipient routing under either
flag setting; start new jobs after the fleet upgrade is complete.
See [ADR-647](adr/647-independent-event-routing-default.md) for qualification,
upgrade, and fallback requirements, and
[ADR-606](adr/606-independent-event-recipient-routing.md) for the ownership model.

Publish acceptance means the event is durably stored. A recipient marked
`enqueued` has been handled by routing; normally it has an invocation, while
work-policy `cancel_pending` creates a cancellation receipt instead; the receipt is `delivered` when all routing
candidates settle, including terminal failures. Handler completion is tracked
by the invocation lifecycle. Handler execution is at least once: use an
idempotency key or version check for side effects. Subscriptions without
`ordered: true` have no publication-order guarantee. Ordered subscriptions
preserve acceptance order within their keyed lane during normal routing and
automatic retries; explicit replay after newer work has advanced can change
that order.

Find recipients waiting to be routed without knowing their event IDs:

```bash
gregale events backlog
gregale events backlog --app analytics --capacity-scope consumer --min-age 10m
gregale events backlog --consumer-kind workflow --state pending --json
gregale events backlog --origin backfill --subscription-id SUB
```

The API is `GET /v1/events/backlog` with `app`, `subscription_id`,
`consumer_kind`, `origin`, `state`, `capacity_scope` and `min_age_seconds`
filters. It lists captured application and workflow recipients, plus recipients
added by durable backfill, waiting in either routing mode. Rows show event
identity, consumer kind, origin, workflow name when applicable, recorded wait
reason, age since acceptance, cumulative deferrals, retry/lease metadata and
receipt links, plus routing history links for application subscriptions.
Workflow admission and run state are available through the receipt. Consumer
counts cover all matching waiting rows, independently of the recipient page.
This view excludes settled
routing and handler execution queues, which remain available through delivery
inspection.

Use `--after` for the recipient continuation and `--consumers-after` for the
independent consumer continuation; the API names the latter `consumers_after`.
Both pages default to 100 and cap at 200; `--consumer-limit` controls consumer
summaries. Keep filters unchanged and use cursors from the same `window_at`
when passing both together. The window anchors acceptance/age filtering, while
membership and counts remain live: recovered recipients disappear, even if
their row supplied the cursor. Replay can restore older work behind a cursor;
restart discovery to include it. Listing oldest first does not promise delivery
FIFO. A whole-event lease appears as `receipt_processing` because it does not
identify which recipient is currently being routed.

The response declares `coverage=captured_and_backfill_recipients` and reports
unresolved older receipts without snapshots as `unattributed_receipts`. That
count is account-wide and uses only the acceptance/age window, even with other
filters. The API requires a read key, returns metadata with no-store caching,
and bounds reads to five seconds. Narrow filters and retry on
`event_backlog_read_timeout`. Apply the [backlog migration](adr/617-event-consumer-backlog-inspection.md)
and consumer origin migration before upgrading the API. Workflow rows represent
routing admission; after admission, inspect the workflow run and its steps for
execution status and recovery. `workflow_routing` identifies a pending workflow
admission that is not currently in a capacity wait, retry delay, or active lease.

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

To discover older retained events matching one current ordinary subscription,
use the read-only historical replay preview:

```bash
gregale events replay-preview APP --subscription-id SUBSCRIPTION_UUID \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --limit 50
```

The API is `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/replay-preview`;
the Go client is `pkg/api.Client.PreviewEventReplay`. The range uses **platform
acceptance time** `[from, until)`, independent of producer event time. Events
accepted before the subscription was created can match its current filter. The
preview returns event metadata and receipt links without creating deliveries.
Work-bound subscriptions, workflow starts and object notification declarations
are outside this subscription-specific preview surface. Workflow starts have a
separate read-only preview described below.

Each page examines at most `--limit` retained envelopes (default 50, maximum
100), including nonmatches. Counts apply to that page. An empty matching page
may still have `next_after`; continue with `--after` and the same app,
subscription and range. The cursor preserves the first page's `cutoff_at` and
subscription revision. A changed or disabled subscription requires restarting
the preview. `original_recipient` distinguishes `captured`, `not_captured` and
legacy `unknown` membership; it does not imply successful delivery or define
which events a future backfill runner would execute.

Settled receipts retain thirty days after **routing settlement**; unresolved
receipts can survive longer. `earliest_retained_at` is account-wide and does not
prove gap-free history. `history_complete` is always false. Retention can remove
rows between pages, and delayed commits of older acceptances can change visible
membership. This preview does not pin events or provide a frozen export. See
[ADR-645](adr/645-subscription-retained-event-replay-preview.md) for the contract.

To inspect historical workflow starts captured when each event was accepted,
use the separate workflow replay preview:

```bash
gregale events workflow-replay-preview APP --workflow-name paid \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --limit 50
```

The API is `GET /v1/apps/{slug}/workflow-event-replay-preview`; the Go client is
`pkg/api.Client.PreviewWorkflowEventReplay`. It scans retained envelopes in the
same half-open platform acceptance-time range and reports whether the named
workflow was captured, whether its captured trigger filter matches, the latest
routing checkpoint, and whether the durable `(outbox, workflow recipient)`
admission receipt exists. A retained run ID and status are included when the
run still exists. The durable admission receipt remains visible after run
retention expires, so a pruned run is still recognized as deduplicated.

`potential_admission_count` counts matching captured recipients without an
admission receipt. It estimates future admission impact; it does not check
current account quota, app availability, or a future replay policy. The preview
does not start workflow runs or create deliveries. It does not evaluate the
current workflow definition for events where the workflow was not captured;
those are reported as `not_captured`, while legacy envelopes without recipient
snapshots are `unknown`. Counts are page-local, `history_complete` remains
false, and continuation is not a frozen export. See
[ADR-793](adr/793-workflow-event-replay-preview.md) for the contract.

To start a currently eligible app workflow for retained events whose immutable
recipient snapshot excluded it, create a durable workflow backfill:

```bash
gregale events workflow-backfill APP --workflow-name paid \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --yes
gregale events backfill-status JOB_UUID
gregale events backfill-items JOB_UUID --state enqueued
```

The job snapshots the current workflow definition and trigger. It considers
only events with known recipient membership, a settled delivered receipt, no
captured membership for this app workflow, and a matching trigger. Unknown
legacy snapshots, captured workflows, unsettled receipts and prior admission
receipts are skipped. The durable `(event, app workflow)` admission receipt
deduplicates future jobs even after run retention removes the linked run.
`enqueued` means the run was admitted; monitor the run separately for
completion. Retryable quota and temporary target failures can be retried with
`gregale events backfill-retry JOB_UUID --yes`. The 30-day range, active-job
quotas, retained-history coverage and pruning protection follow subscription
backfills. See [ADR-794](adr/794-durable-workflow-event-backfill.md).

To create actual independent deliveries for eligible historical events, start
a durable backfill for the same ordinary subscription:

```bash
gregale events backfill APP --subscription-id SUBSCRIPTION_UUID \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --yes
gregale events backfill-status JOB_UUID
```

Backfill uses platform acceptance time and the half-open range `[from, until)`;
the server records a fixed cutoff when the job is created. The requested range
can span at most 30 days. The target must be a current enabled ordinary
subscription; work-bound subscriptions are unsupported. The job snapshots its
target declaration, so later subscription changes do not change that job.

The duplicate policy is `skip_existing`. If the original acceptance snapshot
already captured this consumer, membership is unknown, a target recipient row
already exists, or the original receipt is not settled, that envelope is
skipped. Only matching, settled receipts with a known snapshot and a definitely
absent target can create a new recipient. Gregale leaves the original
`recipient_snapshot` unchanged. Backfill uses the normal independent recipient
lease, capacity, retry and handler lifecycle; it does not establish FIFO
ordering. `enqueued` means the invocation was admitted, not that the handler
completed.

The job scans at most 100 envelopes per page and holds at most 100 pending or
processing target deliveries at once. Up to three jobs can run per account,
with one active job per subscription. Active jobs protect their requested
range from normal settled-receipt pruning. Retryable failed deliveries keep
their source receipts through the job’s 30-day recovery window; other payloads
follow ordinary retention while the job’s per-envelope outcome counts remain
available for 30 days after completion. `earliest_retained_at` is a coverage
warning, not proof of a complete archive; already expired envelopes cannot be
recovered. Poll status until `completed` or `completed_with_failures`; the
status includes a separate `retryable_failed` count. Retry a bounded batch of
eligible routing failures with
`gregale events backfill-retry JOB_UUID --limit 100 --yes`; handler failures,
retries and dead letters continue through their existing lifecycle. Completed
job metadata and per-envelope outcomes are retained for 30 days. See
[ADR-639](adr/639-durable-subscription-event-backfill.md) for the full contract.

Inspect outcomes when you need to locate a failed or skipped event:

```bash
gregale events backfill-items JOB_UUID --state failed --limit 50
```

The command prints a continuation command when another page is available. Keep
the job and state filter unchanged when following its cursor. Each item shows
the event identity, acceptance time, routing state, attempts and bounded
failure details; it never includes event data and remains readable after the
source envelope expires. While the original receipt is retained, `receipt_url`
opens unified delivery inspection; `attempt_history_url` opens the consumer's
handler attempts when its delivery provenance and current app ownership are
available. Links are omitted after their source expires, including if a new
event reuses that source and ID. Item states are live while a job is running, so for a
complete filtered view, inspect after the job reaches a terminal state.

Inspect one published event across captured and backfilled consumers:

```bash
gregale events inspect --source billing.stripe --id evt-123
gregale events inspect --source billing.stripe --id evt-123 --json
```

Publish returns a `receipt_url` and `Location` header for
`GET /v1/events/receipt?source=SOURCE&id=ID`. Identical publish retries keep the
original `accepted_at`. The receipt includes every source/type candidate captured
at acceptance, even before an invocation exists, and separates routing from
handler execution. Whole-snapshot counts cover pending, processing, filtered,
enqueued, and failed recipients. `recipient_count` and `routing_summary` retain
their acceptance-snapshot meaning; `backfill_recipient_count` and
`backfill_routing_summary` count added consumers separately. Recipients label
their `origin` and link to the originating backfill job while it is retained.
Handler outcomes preserve cancellation,
supersession, expiry, and dead letters. A `cancel_pending` operation reports its
cancellation receipt rather than a handler invocation.

Use `--limit` (1–200, default 100) and `--after` with `next_after` for larger
fanouts. Pagination follows captured recipient order, then stable appended
backfill positions even during retries, replay, or job pruning; outcomes can
change between pages. Inspection order does not guarantee delivery order.
`routing_settled_at` means routing
has settled, including failures, and `retain_until` is thirty days later. These
fields are absent while routing is active. Legacy receipts without snapshots
report `snapshot_captured=false`; their membership cannot be reconstructed.
Whole-event routing exposes retained checkpoints, so a pending recipient can
still have an active parent worker.

Each recipient includes its routing attempts, retry time, error, replay count,
and routing history URL, plus the retained original invocation or cancellation.
`record_unavailable` means the original execution record was not found after
routing; it does not assert success. Execution records have independent
retention. The JSON response supplies applicable selective recovery requests;
calling them requires the existing write scopes and rechecks current eligibility.
A retryable backfill routing failure has the same selective routing recovery
action, even while sibling events in the job are pending. Recovery resets just
that delivery and keeps the job item and receipt consistent. Backfill handler
failures use the same handler replay and dead-letter recovery as captured
consumers. See [ADR-646](adr/646-unified-backfill-delivery-inspection.md).
Routing replay and in-place dead-letter replay remain visible on the original
receipt. Generic handler replay creates a new invocation with ledger-owned parent
and root identity. The receipt preserves the original failure and adds `recovery`
with `latest_replay`, `retained_replay_count`, and `history_url`. A completed latest
replay means that replay succeeded; text inspection labels it `recovered`.
Recovery requests target the latest retained replay, and are absent while that
replay is active or completed. Independent consumer outcomes remain separate.

Recover one captured application or workflow consumer, or a backfilled
application consumer, directly from its receipt:

```bash
gregale events recover --source billing.stripe --id evt-123 \
  --subscription SUBSCRIPTION_ID --dry-run
gregale events recover --source billing.stripe --id evt-123 \
  --subscription SUBSCRIPTION_ID
```

The command follows recipient pages automatically and selects the receipt's
current `routing_replay`, `handler_replay`, `keyed_handler_replay`, or
`dead_letter_replay` action. It sends one selective request through the existing
replay API. Successful consumers retain their original deliveries. Routing
recovery uses the accepted subscription snapshot; handler recovery targets the
latest eligible execution in its trusted replay lineage.
For workflow recipients, `routing_replay` recovers admission of the captured
workflow definition. Once admitted, inspect the linked workflow run for step
status and execution recovery. Text inspection shows the workflow name and
retained run ID/status alongside its routing outcome.

`--dry-run` reads the receipt without changing delivery state. It reports the
selected action or explains why recovery is unavailable, including successful
consumers, active deliveries/replays, cancellation, unavailable execution
records, and workflows whose admission already completed. A dry run exits zero
when it can inspect the selected recipient, including when no action is available.
Actual recovery exits nonzero if the receipt offers no action. Missing recipients, legacy
receipts without captured membership, unsupported actions, and API failures
also exit nonzero. `--json` emits event/subscription identity, `dry_run`,
`status` (`available`, `queued`, or `unavailable`), the selected `action`, and
the replay `result` or an unavailable `reason`.

Availability can change after inspection. The replay endpoint rechecks current
ownership, scopes, deadlines, and claim eligibility; a rejected request is
reported without trying another recovery path. `queued` means recovery was
accepted, not that the handler completed. Use `events inspect` and `events
attempts` to follow it. At-least-once delivery and application side-effect
deduplication still apply.

Plain handler replay uses `POST /v1/invocations/{id}/replay` (or
`gregale invocations get --replay INVOCATION_ID`). Each failed parent creates
one durable child. Repeated and concurrent requests return that child even
with different request keys or after success; further recovery targets the
failed child. Customer self-service and account-operator replay share this
identity and preserve the captured customer and environment. If the child has
been pruned, its retained parent returns `invocation_replay_unavailable` and
receipts suppress its handler replay action. Existing accepted replay IDs
remain readable when an old deployment pin expires. Delivery remains at least
once; applications still deduplicate external side effects. See
[ADR-612](adr/612-durable-plain-invocation-replay.md).

Failed keyed handlers use `keyed_handler_replay`, which calls
`POST /v1/invocations/{id}/replay-keyed`. It preserves the captured policy
revision, key, fairness controls and environment, and joins the end of that
key's queue. It does not supersede newer pending work or restart debounce.
The original pending expiry remains effective: expired work needs a new
publication with a new event ID and fresh lifetime. Retrying the same accepted
event does not renew its deadline. Generic invocation replay rejects keyed
work so it cannot bypass its claim gate.

```bash
gregale invocations get --replay-keyed INVOCATION_ID
```

Repeated keyed recovery requests return the same child, even after it completes.
To recover again, target that child after it fails. If the child has been pruned
while the parent remains, the parent cannot create another execution; receipts
suppress that action. Queue-bound dead letters retain their existing in-place
replay path. See [ADR-609](adr/609-safe-keyed-invocation-replay.md) for ordering,
expiry and retention behavior.

Keyed dead-letter replay keeps the original receipt and sequence. It waits
for any same-key invocation or broker delivery already running, including a
later sequence. An expired lease must be recovered before the replay can
proceed. Once that ownership is resolved, pending work follows sequence order;
other keys remain eligible. Replay preserves the original pending expiry.
See [ADR-610](adr/610-keyed-dead-letter-replay-claim-exclusion.md).

Inspect the original handler's delivery attempts and its trusted replay children:

```bash
gregale events attempts --source billing.stripe --id evt-123 \
  --subscription SUBSCRIPTION_ID --limit 100
```

This reads `GET /v1/events/receipt/attempts`; receipt recipients expose its
`attempt_history_url`. Each entry contains invocation ID, replay generation,
attempt number, start and finish times, outcome, error and next retry time.
An expired dispatch lease settles as `unknown`: the history does not prove
whether the handler ran or applied side effects. Use application idempotency.
Pagination uses the returned `next_after` with `--after`, newest attempt first.

Only recorded, retained attempts are shown. History starts with claims made
after the attempt-ledger upgrade and is not backfilled. Closed attempts expire
after at most 30 days, earlier for shorter result retention or invocation
deletion; running attempts are not pruned. An empty history does not establish
that no delivery occurred. See [ADR-611](adr/611-invocation-backed-event-attempt-history.md).

List a consumer's retained replay executions, newest first:

```bash
gregale events inspect --source billing.stripe --id evt-123 --subscription SUB
gregale events inspect --source billing.stripe --id evt-123 --subscription SUB --json
```

This reads `GET /v1/events/receipt/replays` with the same source, ID and captured
`subscription_id`. Use `--limit` and the returned `next_after` with `--after` to
read older pages. Replay cursors are separate from recipient cursors and remain
usable when their execution anchor expires. Root identity and its creation time
survive on descendants, so expired intermediate rows cannot sever recovery or
attach it to a later reuse of the event identity. Counts cover retained rows,
not lifetime replays. When all replay records expire, absence of `recovery` does
not establish that recovery never happened. Older generic replays without trusted
lineage remain in app delivery history; guest headers cannot reconstruct it.

Captured target IDs remain, but current metadata and recovery actions
are unavailable when the target no longer belongs to the account.

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

History is ordered newest first. Each recipient retains at most 128 detail rows
and 64 KiB of logical detail bytes; unprotected detail expires after thirty days,
including while the event remains pending. Compaction prioritizes the latest
outcome, real failure and replay request. Error details are bounded to 1,024 UTF-8
bytes and clipped rows set `details_truncated`.

Repeated consecutive capacity waits with the same scope update durable counters
instead of appending detail. JSON output includes `coverage=bounded_recorded_outcomes`
and recipient `summaries`: cumulative deferrals, wait first/last times, coalesced
and compacted counts, retained records/bytes, and the boundary of removed detail.
Summaries describe recorded observations and survive with the receipt; older
unrecorded transitions cannot be reconstructed. The last capacity scope is the
most recent recorded wait, including after recovery.

Use `--subscription-id` to narrow the view and `--before` with `next_before` from
JSON output to page through older rows. Cursors remain valid when their detail
row is removed, although an older page may become empty. Summaries reflect current
observations independently of the page cursor. Recovery uses the durable recipient
checkpoint and does not depend on retaining every history row. See
[ADR-616](adr/616-bounded-event-routing-history.md).

To retry one terminal pre-invocation failure, pass its event ID, source, and
subscription ID from the failure row:

```bash
gregale events replay APP \
  --event-id evt-123 \
  --event-source billing.stripe \
  --subscription-id 5ef2a270-2c12-4ddd-a2a7-a0873995f7c8
```

For receipts using independent recipient routing, replay is available as soon
as that recipient fails, even while siblings are active. Legacy receipts must
wait until the event's fanout receipt settles. Replay queues only that recipient
and keeps the event payload and recipient configuration
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
event can accept additional replay batches while pending. Independent recipient
routing also permits replay while siblings are processing. A legacy event with
an active whole-event worker is left alone and continues to report more
failures; repeat after that event settles.
Configuration failures such as an invalid
subscription or unavailable target remain untouched for explicit repair and
single-recipient replay.

The machine-readable event contract is published in
[`api/asyncapi.yaml`](../api/asyncapi.yaml), including the authenticated
`POST /v1/events:publish` ingress.

### Diagnosing ordered routing waits

`gregale events backlog --waiting-reason ordering_blocked` lists recipients waiting for earlier routing in their captured app/policy/key lane. Filtering happens before recipient pagination and consumer aggregation. Consumer summaries include `ordering_waiting_recipients`.

Each blocked recipient includes `ordering_blocker`: the earliest unresolved event and subscription, acceptance time and age, current routing state, next retry time when pending, and a receipt URL. Event data and resolved work keys are not exposed. The lookup uses the same predicate as recipient claims and is recomputed on each read, so recovering a blocker clears the wait without a projection update.

Active routing and shared receipt leases take precedence over subscription controls and ordering; live subscription controls precede ordering, which precedes recorded capacity and retry backoff. These diagnostics describe event routing, not an invocation already admitted to its execution lane. Whole-receipt routing excludes earlier positions in its own snapshot because it walks those positions serially.

### Bulk recovery of failed application consumers

Use a recovery preview to select retained terminal **routing** failures captured
at publication. Filters are exact subscription ID, event source, event type,
failure classification, and minimum time since the recorded failure. Retryable
failures are selected by default; `--include-non-retryable` explicitly broadens
the selection.

```sh
gregale events recovery-preview invoice-worker \
  --event-type invoice.created --failure-code invocation_enqueue_failed --min-age 10m

gregale events recovery-create invoice-worker \
  --event-type invoice.created --failure-code invocation_enqueue_failed --min-age 10m \
  --rate 10 --yes

gregale events recovery-status JOB_ID
gregale events recovery-items JOB_ID --limit 100
gregale events recovery-cancel JOB_ID --yes
```

The preview is read-only and returns at most 100 sample recipients. Its match
count is exact up to 10,000; `exceeds_job_limit` means the reported count is a
lower bound and the filters must be narrowed. Creation captures its own current
selection and rejects more than 10,000 recipients without creating a partial
job. New failures after creation are excluded. An empty selection completes
immediately.

Jobs are durable and account scoped. At most three jobs may run per account.
Each job processes at most 1–100 recipients per one-second window (default 10),
including skipped items; the persisted budget survives restarts and concurrent
workers. Processing may be slower under scheduler load. Jobs expire after 24
hours, cancelling remaining items. Completed/cancelled jobs and their
metadata-only item outcomes are retained for 30 days.

Each retry checks the selected recipient's failure identity before atomically
reopening it and recording `queued`. If another recovery already changed it,
the item becomes `skipped` with reason `changed`; missing retained receipts and
deleted targets are also skipped. Legacy whole-receipt routing claims defer
recovery until they settle. No new event identity is published, immutable
recipient snapshots are preserved, and successful sibling consumers keep their
outcomes. Existing work ordering and deduplication rules apply. Replaying an
older terminal failure cannot undo younger deliveries that already completed.

Cancellation and processing share a job lock. Cancellation waits for any retry
transaction already in progress, then cancels remaining selected items. Already
queued retries continue through normal delivery. `completed` means every
selection item was queued or skipped; it does not mean invocation execution
succeeded. Inspect receipts and existing attempt history for delivery outcomes.

API endpoints:

- `POST /v1/apps/{slug}/event-recoveries/preview`
- `POST /v1/apps/{slug}/event-recoveries` (supports `Idempotency-Key`)
- `GET /v1/event-recoveries/{jobID}`
- `GET /v1/event-recoveries/{jobID}/items?after=POSITION&limit=100`
- `POST /v1/event-recoveries/{jobID}/cancel`

Preview and reads require `apps:read` or `admin`; creation and cancellation
require `deploy:write` or `admin`, with the existing MFA policy. Responses use
`coverage: captured_application_recipients`. Historical backfill recovery,
workflow recovery, and failures after invocation admission retain their existing
recovery APIs; they are excluded from this job selection. Envelope data, work
keys, and raw errors are not returned or copied into recovery item metadata.
Receipt retention is independent: an expired receipt is skipped rather than
reconstructed from the job.

### Pause and drain a subscription

Pause a captured application consumer during maintenance or an outage:

```sh
gregale events subscription-pause invoice-worker SUBSCRIPTION_ID --yes
gregale events subscription-status invoice-worker SUBSCRIPTION_ID
gregale events backlog --app invoice-worker --waiting-reason subscription_paused

gregale events subscription-resume invoice-worker SUBSCRIPTION_ID --rate 10 --yes
gregale events backlog --app invoice-worker --waiting-reason subscription_rate_limited
```

Pausing stops new routing admissions for that subscription ID. It does not
change subscription matching: enabled declarations continue capturing events.
Other consumers continue according to their existing capacity and ordering
rules. Invocations already admitted, including their execution retries,
continue. Pause and admission serialize on the same consumer lock; a successful
pause response follows any admission transaction already in progress.

Pause and drain rate are live operator controls stored independently from
manifest declarations. They survive restarts, redeployment and subscription
removal. A removed subscription with retained controls can still be inspected
and resumed using its captured ID. A newly created subscription ID has its own
controls. Deleting a target app lets its waiting recipients settle as
unavailable targets rather than holding them indefinitely.

Resume defaults to ten new routing admissions per one-second window. Rates
1–100 are supported; `--rate 0` removes pacing. This rate also applies to future
publications after the backlog drains. The persisted budget is shared by all
workers and is reserved in the admission transaction, so rollbacks restore it.
Repeating a resume with the same active rate does not reset the budget.
Normal claim polling and previously scheduled retry times may delay draining.
Pause and rate waits do not consume the recipient's routing retry budget.

Status reports pending and processing recipient counts, the oldest pending
acceptance time and age, pause state, and rate. Counts cover retained publication
and historical backfill recipients for that application subscription. Backlog
filters run before pagination and consumer aggregation. Active routing and
shared receipt leases take precedence in waiting reasons; live subscription
controls precede ordering, recorded capacity and retry backoff.

Pending captured receipts are retained until routing settles; pausing does not
start an expiry timer. Paused events consume the existing account retained-event
and byte budgets. Once those budgets are full, publication is rejected by the
normal storage admission rules. Resume retains original event identities and
snapshots, and uses the existing ordering and deduplication behavior. Settled
receipt retention and invocation retention follow their existing rules.

API controls are available at:

- `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control`
- `POST /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/pause`
- `POST /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/resume`
  with `{ "rate_per_second": 10 }` (or `{}` for the default).

Reads require `apps:read` or `admin`; writes require `deploy:write` or `admin`,
with the existing MFA policy. Controls apply to captured application routing
admission, including application backfill recipients. Workflow and object
notification recipients have their own delivery paths. Pre-snapshot legacy
receipts use their original dynamic routing path.

### Consumer routing health and maintenance alerts

Inspect one application's subscription:

```sh
gregale events subscription-health my-app <subscription-id> --window 15m
```

The health endpoint at
`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/health?window=15m`
reports live pending and processing counts, oldest pending age, pause duration,
scheduled retries per second, terminal failure percentage, successful routing
rate, and acceptance-to-routing p95 latency. Routing ends at invocation admission;
execution completion is observed separately. Historical backfill latency includes
the event's original age. Supported windows are `5m`, `15m`, `1h`, `6h`, and `24h`.

Rates count retained `fanout_attempt` and `backfill_attempt` outcomes. Terminal
failure percentage uses failed / (failed + enqueued); recovery can record more
than one outcome per recipient. Retry rate counts error outcomes returning to
pending and excludes capacity, pause, and pacing waits. Coverage is
`bounded_recorded_outcomes`. `history_compacted=true` means windowed rates are
incomplete retained samples. These are consumer API observations; they do not
create subscription-labelled Prometheus series.

Create an alert with `gregale alerts add --event-subscription-id <uuid>` and one
of these metrics:

| Metric | Unit |
| --- | --- |
| `event_pending_recipients` | Pending routing recipients |
| `event_oldest_pending_seconds` | Age of oldest pending recipient |
| `event_retry_rate_per_second` | Recorded retries scheduled / second |
| `event_terminal_failure_pct` | Recorded terminal failure percentage |
| `event_routing_latency_p95_seconds` | Acceptance-to-routing p95 seconds |
| `event_paused_seconds` | Continuous duration of current pause |
| `event_drain_rate_per_second` | Recorded successful routes / second |

For example, use `--metric event_oldest_pending_seconds --comparison gt
--threshold 600 --window-spec 15m` with the usual app, name, webhook URL, and
secret flags. A failure alert can use `event_terminal_failure_pct`, `gt`, and
`5`. Consumer rules require webhook actions, windows up to 24h, and an immutable
subscription selector. Failure percentages need 20 terminal observations; latency
needs one success. Drain-rate alerts require a pending or processing backlog.
History-dependent alerts skip compacted windows.

Pausing suppresses all consumer health alerts except `event_paused_seconds`.
A separate pause alert detects maintenance that runs too long. Suppressed or
insufficient observations set the rule to `unknown`; existing queued webhook
notifications continue through the alert delivery outbox. Repeated pause requests
preserve pause duration; resume resets it.

### Per-consumer routing retry policies

Application subscriptions can configure routing retries in YAML:

```yaml
event_triggers:
  - source: orders
    type: order.created
    retry:
      max_attempts: 20
      initial_backoff: 2s
      max_backoff: 2m
      max_retry_duration: 30m
      jitter: true
```

The equivalent TOML declaration uses `[triggers.event.retry]` after its
`[[triggers.event]]` entry. A retry block defaults to 12 attempts, 5s initial
backoff, a 5m cap, unlimited duration budget, and jitter enabled. Backoffs use
whole milliseconds up to 1h; attempts range from 1 to 100 and duration budgets
from 0 to 7d. Maximum backoff must be at least the initial delay.

Inspect or replace settings for future events:

```sh
gregale events subscription-retry-status my-app <subscription-id>
gregale events subscription-retry-set my-app <subscription-id> --max-attempts 20 --initial-backoff 2s --max-backoff 2m --max-retry-duration 30m --yes
gregale events subscription-retry-reset my-app <subscription-id> --yes
```

GET, PUT, and DELETE at
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/retry-policy` inspect,
replace, or reset current subscription configuration. API replacement uses
`max_attempts`, `initial_backoff_ms`, `max_backoff_ms`,
`max_retry_duration_ms` (default 0), and `jitter` (default false). Reads require
`apps:read` or `admin`; writes require `deploy:write` or `admin` and MFA.

The duration budget counts failed routing attempt time and reserves scheduled
retry delays. A retry is scheduled only if its entire delay fits. Pause, pacing,
capacity, ordering, and worker downtime add no budget cost. This budget does not
impose an event age deadline or interrupt an in-flight admission. Delay grows
exponentially up to the configured cap; jitter spreads it between 1ms and that
cap using a stable per-recipient, generation, and attempt hash.

Policies are captured at event publication or backfill job creation. Changing
or resetting configuration leaves those snapshots intact. Reapplying a manifest
without a retry block restores legacy defaults for future events: 12 attempts,
5s initial backoff, 5m maximum, no duration bound, and jitter disabled. Legacy
snapshots use those same defaults. Apply the migration and upgrade all routing
workers before enabling custom policies.

Receipt routing diagnostics expose the captured `routing_retry_policy`,
`retry_spent_ms`, and `retry_stop_reason`. Retained routing attempt history
also records stop reason: `non_retryable`, `max_attempts`, or `max_duration`.
The underlying `failure_code` remains visible. Operator replay starts a fresh
retry generation under the same snapshot, preserving ordering and deduplication.
Invocation execution retries have their separate existing policy.

### Automatic consumer circuit breakers

Enable an opt-in circuit breaker for an application event subscription:

```sh
gregale events subscription-circuit-set my-app <subscription-id> --yes
gregale events subscription-circuit-status my-app <subscription-id>
gregale events subscription-circuit-reset my-app <subscription-id> --yes
gregale events subscription-circuit-disable my-app <subscription-id> --yes
```

Defaults open a circuit at 50% routing failures with at least 20 outcomes in
300 seconds. Configure `--failure-threshold-pct`, `--min-samples`,
`--window-seconds`, `--cooldown-seconds`, `--probe-successes`,
`--recovery-max-rate`, and `--recovery-seconds`. Configuration is operational;
deployments do not declare or overwrite it.

An open consumer waits for the cooldown (default 60s). Available pending work
then probes one delivery at a time. Three successful admissions start paced
recovery: one routing permit per second, doubling every 10s to the configured
cap (default 10/s). After the recovery interval (default 60s), it closes. A
probe or recovery routing failure reopens it. A lost probe lease also reopens
it when no durable outcome exists. Filtered events and capacity waits release
a probe without counting as success or failure. Healthy consumers continue.

Breaker waits spend no routing retry attempts or duration budget. It measures
routing failures before invocation admission; execution retries and failures
retain their existing policy. It does not replay dead letters automatically.
Existing in-flight routing may finish, and a permit consumed by a neutral
outcome can reduce actual throughput. Ordering and deduplication still apply.

Status, delivery controls, and consumer health expose durable state, transition
reason, cooldown, probe status, recovery rate, and incomplete history. Backlog
waiting reasons include `circuit_open`, `circuit_probe_wait`, and
`circuit_recovery_rate_limited`. State transitions occur with routing work;
reading status does not schedule probes. Threshold checks occur at most once
per second per consumer. Compacted observation windows prevent automatic
threshold decisions and recovery completion.

Manual pause always stays in effect until explicitly resumed. Reset closes an
enabled breaker with a fresh observation window; disable removes it. Both
preserve manual pause and pacing. Enabling or replacing policy also starts a
fresh observation window. Terminal failures remain recoverable using the
existing replay and recovery commands.

The REST resource is
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/circuit-breaker`: GET
reads, PUT enables/replaces with defaults for omitted fields, DELETE disables,
and POST to `/reset` closes an enabled breaker. Policy fields are
`failure_threshold_pct`, `min_samples`, `window_seconds`, `cooldown_seconds`,
`probe_successes`, `recovery_max_rate_per_second`, and `recovery_seconds`.
Reads require `apps:read` or `admin`; writes require `deploy:write` or `admin`
and MFA. Apply the migration and upgrade all routing workers before enabling.

### Per-consumer delivery age limits

Set a wall-clock limit for newly admitted event deliveries:

```yaml
event_triggers:
  - source: inventory
    type: stock.changed
    retry:
      max_delivery_age: 15m
```

The same field is supported under `[triggers.event.retry]` in TOML. It defaults
to zero (no expiry), supports whole milliseconds up to 720h, and is captured
with each event recipient. Configure future events through the existing retry
policy API using `max_delivery_age_ms`, or the CLI:

```sh
gregale events subscription-retry-set inventory-api <subscription-id> --max-delivery-age 15m --yes
```

This command replaces the routing policy; include other retry flags to retain
custom settings. Resetting the policy disables expiry for future events.
Captured events keep their original policy. Strictly ordered subscriptions
reject positive delivery age limits.

Age starts when Gregale accepts the event, using the platform timestamp. Pauses,
capacity waits, circuit cooldowns, retry backoff, and worker downtime all count.
Once the limit is reached, pending routing becomes a terminal failure with
`failure_code` and `retry_stop_reason` set to `delivery_expired`. The scheduler
can select it for expiry even while normal delivery is paused or circuit-blocked.
Active claims remain fenced until they finish or their leases are recovered.
Expiry spends no extra retry attempt or duration budget, and other consumers
continue independently. Already admitted invocations keep their execution lifecycle.

Receipt diagnostics show `delivery_deadline_at` and `delivery_age_override`.
Consumer health reports `expired_deliveries` in its observation window; these
are included in terminal failures. Expiry does not count as a routing failure
for circuit breakers. Retained failed-delivery listings and history keep the
expiry reason for inspection.

Replay preview includes `delivery_expired` and `delivery_deadline_at` for each
match, plus `expired_count`, under the current target policy. A historical
backfill uses its captured policy against the original event acceptance time.
To deliberately deliver stale work, request an explicit override:

```sh
gregale events replay inventory-api --event-source inventory --event-id evt-123 --subscription-id <subscription-id> --allow-expired
gregale events backfill inventory-api --subscription-id <subscription-id> --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z --allow-expired --yes
```

The equivalent replay/backfill request field is `allow_expired: true`.
Individual replay returns `409 Conflict` without it when the original deadline
has passed. The override applies to that replay generation, or to the explicitly
created historical backfill job. It is persisted and audited, while ordering,
deduplication, manual pause, and circuit controls still apply. Bulk retry and
recovery never silently override age; recovery skips items that expire after
selection. Apply the migration and upgrade all routing workers before enabling.

## Select schema versions per application consumer

Use `schema_versions` to upgrade consumers independently of event producers:

```yaml
triggers:
  event:
    - source: billing.*
      type: invoice.paid
      schema_versions: [v1, v2]
```

An omitted or empty list accepts every version, including unversioned events.
A nonempty list accepts only exact, case-sensitive identifiers and excludes
unversioned events. Select up to 16 unique versions; each identifier is 1–64
characters, starts with a letter or digit, and contains letters, digits, dots,
underscores, or hyphens. Versions may be selected before registration.

```sh
gregale events subscription-versions-status <app> <subscription-id>
gregale events subscription-versions-set <app> <subscription-id> --versions v1,v2 --yes
gregale events subscription-versions-reset <app> <subscription-id> --yes
```

The API resource is
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/schema-versions`: GET
inspects the selection, PUT accepts `{ "schema_versions": ["v1", "v2"] }`,
and DELETE resets it. PUT requires an array; an empty array accepts all versions.

Selection is captured when an event is published, or when a retained backfill
job is created. Updating a subscription affects future captures. Manifest
omission resets the selection. Existing captured deliveries without a selection
accept every version. Workflow and object notification subscriptions use their
existing behavior.

Incompatible versions settle as `filtered` with
`filter_reason: schema_version_mismatch`, create no invocation, consume no
routing retry attempt, and do not count as circuit breaker failures. Filtering
precedes pause, backoff, ordering, circuit, and expiry gates. Routing and retained
replay previews report `schema_version_mismatch_count`; backfills skip incompatible
versions as filtered items. Retained replay cursors become invalid when the
subscription selection changes.

## Preview a schema rollout

Before switching a producer to a new version, check consumer version coverage
and validate event data with a read-only rollout preview:

```sh
gregale events schema-rollout-preview billing.stripe invoice.paid \
  --version v2 --schema @invoice-v2.schema.json \
  --samples @invoice-samples.json \
  --from 2026-10-01T00:00:00Z --until 2026-10-08T00:00:00Z \
  --retained-limit 100
```

`invoice-samples.json` is an array of event **data** values, such as
`[{"amount":150,"currency":"TRY"}]`. Omit `--schema` to check an already
registered version. Schema and samples accept inline JSON, `@file`, or `-`
for stdin; only one input may use stdin. `--json` returns the complete response.

The API is `POST /v1/event-schemas:preview-rollout`, available to read keys
(`apps:read` or `admin`) with the same MFA requirements as schema inspection:

```json
{
  "source": "billing.stripe",
  "type": "invoice.paid",
  "version": "v2",
  "schema": {
    "type": "object",
    "required": ["amount", "currency"],
    "properties": {
      "amount": {"type": "number"},
      "currency": {"type": "string"}
    }
  },
  "samples": [{"amount":150,"currency":"TRY"}]
}
```

The response lists enabled application consumers whose source/type patterns
match, with `accepts_version` and `content_filter_present`, plus accepting and
excluding counts. Version acceptance alone does not evaluate content filters
or guarantee delivery. At most 1,000 consumers are returned; when
`consumers_truncated` is true, counts cover only the returned consumers.
Workflow and object notification subscriptions are outside this coverage.

Supply both `from` and `until` to check retained payloads. These are inclusive
start and exclusive end platform acceptance times; future end times are capped
at `observed_at`. The preview scans the newest 1,000 **account** receipts in
that range, including unrelated event types, then checks up to 100 matching
payloads, or the lower `retained_limit`. It validates original event data
against the proposed schema regardless of the original version.

`retained.scanned_count` counts account candidates; `examined_count` counts
matching results. Valid, invalid, and unreadable counts are separate. Envelopes
larger than 64 KiB, malformed envelopes, or inconsistent identities are
unreadable rather than schema-invalid. The aggregate validation byte budget is
4 MiB. Limit exhaustion sets `retained.truncated`; `history_complete` is always
false because retained sampling cannot establish complete historical coverage.
An empty sample is not evidence of compatibility.

Caller samples are limited to 20 data values of 64 KiB each; schemas are limited
to 64 KiB and cannot fetch external references. The request body is limited to
2 MiB and the observation times out after 15 seconds. Validation diagnostics
return a bounded first failing field path and reason, without payload values.
The preview registers no schema, publishes no event, and changes no delivery.
Go, Node, and Python clients expose `previewEventSchemaRollout` (following each
SDK's naming conventions).

## Consumer execution health

Routing health ends at admission. Inspect what admitted handlers do separately:

```sh
gregale events subscription-execution-health my-app <subscription-id> --window 15m
```

The API is
`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-health?window=15m`.
It uses the same read scopes and MFA requirements as routing health. Windows
are `5m`, `15m`, `1h`, `6h`, and `24h`. Go, Node, and Python SDKs expose the
corresponding execution-health operation.

Current counts separate queued, running, retrying, succeeded, failed, expired,
dead-lettered, cancelled, superseded, and unknown executions. Running includes
claimed/dispatching work and its wake waits. Pending executions with attempts
in the current generation count as retrying; other pending executions are queued.
Handler replay invocations and admitted backfill deliveries are included. Counts
cover retained executions, not unique events or only arrivals within the window.

`handler_failure_pct` uses retained finished attempts in the window:
`(retry + failed + dead_letter) / (succeeded + retry + failed + dead_letter)`.
Unknown attempts are reported separately. `window_dead_letters` counts retained
dead-letter attempt outcomes; `dead_letter_rate_per_second` divides that count by
the window duration. Replays can contribute additional outcomes, so this rate
measures formation rather than net backlog growth.

`completion_latency_p95_seconds` measures original event acceptance to successful
completion for retained invocations completing in the window. It includes routing,
queue waits, handler retries, original backfill age, and replay delay. A retained
replay child contributes its own completion; an in-place replay exposes the
invocation's latest completion.

Coverage is `bounded_retained_execution_roots`. The observation selects the newest
1,000 retained admitted roots and at most 5,000 linked invocations. `truncated`
marks a bound; `missing_roots` identifies absent original invocation rows.
Pruned receipts, invocations, and attempt history cannot be reconstructed, so
`history_complete` is always false and rates describe retained observations.

New subscription alert metrics are:

| Metric | Meaning |
| --- | --- |
| `event_execution_dead_letters` | Current retained dead-letter executions |
| `event_execution_dead_letter_rate_per_second` | Recorded dead-letter attempt outcomes per second |
| `event_handler_failure_pct` | Retained handler attempt failure percentage |
| `event_completion_latency_p95_seconds` | Acceptance-to-completion p95 seconds |

For example, use `gregale alerts add` with the app and webhook flags,
`--event-subscription-id <uuid> --metric event_handler_failure_pct
--comparison gt --threshold 5 --window-spec 15m`. These rules require webhook
actions and the supported windows. Failure percentage requires 20 retained
success/failure observations; latency requires one completion. Missing roots,
truncation, unknown current states, or no retained roots make execution rules
`unknown`; attempt-based rules also reject unknown attempts. Routing pauses do
not suppress execution alerts because admitted handlers can still fail.

Cancellation-only event work has a durable cancellation proof instead of a
handler invocation. Execution health excludes these actions from its roots;
its cancelled count describes invocation cancellation, not cancellation commands.


### Paced recovery after handler admission

Use the same durable jobs with `--mode execution` to recover the latest retained
failed handler or dead letter for each application event consumer:

```sh
gregale events recovery-preview invoice-worker --mode execution \
  --subscription-id SUBSCRIPTION_ID --event-type invoice.created \
  --outcome dead_letter --min-age 10m

gregale events recovery-create invoice-worker --mode execution \
  --subscription-id SUBSCRIPTION_ID --event-type invoice.created \
  --outcome dead_letter --min-age 10m --rate 5 --yes

gregale events recovery-items JOB_ID
gregale events recovery-cancel JOB_ID --yes
```

Omit `--outcome` to select both replayable `failed` and `dead_letter` executions.
Routing failure-code and non-retryable filters cannot be combined with execution
mode. API clients send `mode: execution` and optional `outcome` to the existing
`/v1/apps/{slug}/event-recoveries/preview` and `/event-recoveries` endpoints.
The Go, Node, and Python clients expose these fields.

Execution coverage is `retained_application_executions`: admission receipts and
execution rows must still be retained. Materialized backfill consumers are
included; workflows, object notifications, cancellation commands, environment
work, customer operations, and failed queue-bound handlers are excluded. Bound
work in the unified dead-letter ledger can be recovered through its existing
in-place replay. A pruned replay child never authorizes another child for the
same parent. A newer active or successful execution prevents selection of an
older failure. Broad selections may time out; narrow by consumer/type/source.

The existing preview sample, item/job limits, pacing, cancellation, expiry, and
retention apply across both modes. Items include `invocation_id`, identifying
the selected failed execution. Each replay rechecks its frozen failure identity;
changed generations, already replayed work, deleted targets, pruned receipts,
and expired keyed work are skipped independently. Capacity waits keep the item
pending and retry after five seconds; they may delay the rest of that job.

Plain/keyed handler recovery preserves captured headers and retry policy,
renews original execution/result durations for the child, and retains absolute
work/start deadlines. Captured deployment pins are preserved. Dead-letter
recovery uses the unified in-place replay. Keyed lane ordering, replay-child
identity, capacity admission, and successful sibling outcomes are preserved.
A routing pause does not block this explicit execution recovery. Cancelling a
job stops remaining admissions; already queued handlers continue. `queued` and
job `completed` do not imply handler success. Consumers still need idempotent
business effects. See [ADR-806](adr/806-paced-event-execution-recovery.md).


### Inspecting handler results after recovery

Execution recovery now records the exact replay invocation and generation when
it admits each item. Inspect them through the existing commands:

```sh
gregale events recovery-status JOB_ID
gregale events recovery-items JOB_ID --limit 100
gregale --json events recovery-items JOB_ID --limit 100
```

Job status includes an `execution` summary with queued, running, retrying,
succeeded, failed, dead-lettered, expired, cancelled, superseded, and unknown
counts. These counts cover admitted execution-mode items and sum to both
`execution.tracked_count` and the job's admission `queued_count`. Item output
includes `replay_invocation_id`, `replay_generation` (including zero), and an
`execution` observation with state, attempts, observation time, optional
completion time, and evidence source. Routing recovery, previews, and items
that were not admitted omit execution observations.

`execution.source` is `recovery_result` when confirmed terminal evidence has
been saved for this exact replay. Saved results include `recorded_at` (capture
time) and `evidence_source` (`invocation` or `attempt_history`), while
`observed_at` remains the time of the current read. Otherwise the source is
`invocation` for the retained generation, `attempt_history` for retained attempt
evidence, or `unavailable` when neither is present. Later manual replay generations and child
invocations cannot replace this job's tracked outcome. Missing or expired
history, uncertain outcomes, and admissions made before tracking was introduced
report `unknown`. A retry attempt alone does not prove a final result; an
expired or superseded invocation without saved evidence may become unknown
once its row is pruned. Confirmed saved results survive invocation and attempt
history pruning until their recovery job is pruned. `execution.saved_results`
in the job summary counts this subset of `tracked_count`; it is not another
state bucket. Item output shows the evidence source and capture time.

Job `state`, `queued_count`, and `completed_at` continue to describe admission.
For example, a job can be `completed` while its execution summary shows ten
running handlers, or eight successes and two dead letters. Cancelling a job
stops remaining admissions while already admitted handlers continue and remain
observable. Handler success does not guarantee exactly-once business effects.
The API and Go/Node/Python SDKs return these fields through the existing job and
item endpoints. See [ADR-807](adr/807-recovery-execution-outcomes.md).


### Durable terminal recovery results

Terminal results are captured atomically with invocation transitions, including
completion, failure, dead-lettering, expiry, cancellation and supersession. The
saved identity includes invocation id, replay generation and creation time,
scoped to the recovery job's account and app. The first confirmed result wins;
a later replay cannot replace this job's earlier result. Capturing also covers
identity registration and terminal evidence before invocation deletion or an
in-place generation change. Pending, running, retry and uncertain states never
create a saved result. Missing completion timestamps remain absent.

Read existing commands; no write or refresh is needed:

```sh
gregale events recovery-status JOB_ID
gregale events recovery-items JOB_ID --limit 100 --json
```

Saved result metadata follows the existing recovery job lifetime and pruning:
terminal jobs are retained for 30 days after admission completion/cancellation.
Handler completion does not extend that deadline. Job state continues to describe
admission, so a completed job may still have running handlers. Saved results do
not keep invocation rows, payloads, errors or attempt history alive, and they do
not prove exactly-once business effects. Results are removed with their owning
recovery items when the job is pruned. Receipt holds remain a separate opt-in.

The migration backfills only exact tracked identities with retained confirmed
terminal evidence. If that exact invocation is absent or has advanced to another
generation, only the latest still-retained finished terminal attempt qualifies.
History fallback also requires the retained invocation owner to prove the
original creation time, preventing attribution to a reused identifier.
An uncertain exact invocation, later running/retry/unknown attempt, expired
history or legacy admission without a tracked identity cannot produce a saved
result. There is no reconstruction of already-lost outcomes.

Apply the migration before upgrading API and scheduler binaries. Upgrade SDK
consumers that validate `execution.source` to accept `recovery_result` first.
Reads remain
read-only repeatable-read snapshots; no polling worker or new endpoint is added.
Downgrade binaries before migration rollback; rollback deletes saved results and
can return observations to `unknown` when underlying history has expired. See
[ADR-832](adr/832-durable-recovery-terminal-results.md).

### Protect receipts during bulk recovery

Opt in when creating a routing or execution recovery job:

```sh
gregale events recovery-preview APP --protect-receipts
gregale events recovery-create APP --protect-receipts --rate 2 --yes
gregale events recovery-preflight JOB_ID
gregale events retention --app APP
```

API and SDK requests set `protect_receipts: true` on the existing recovery
creation endpoint. Go uses `EventRecoveryRequest.ProtectReceipts`, Node uses
`EventRecoveryRequest.protect_receipts`, and Python uses
`EventRecoveryRequest(protect_receipts=True)`. The immutable `selection` records
the opt-in. Omitted or false preserves the existing unprotected behavior.
Preview never holds receipts; creation selects and protects currently retained
receipts atomically while serialized with pruning. A preview cannot reserve
receipts or restore events already pruned before creation.

Protection applies only to pending items while the job is running or paused and
before its original 24-hour `expires_at`. A pending item releases its hold when
its replay is admitted or it is skipped or cancelled. Job completion,
cancellation or expiry releases all remaining holds. Expiry releases holds even
before the worker finalizes the stored job state. Pause and rate changes never
extend expiry; an existing job cannot enable protection afterward. Overlapping
jobs retain their independent holds until each releases them.

Held receipts continue counting toward account storage limits, so protection can
increase storage pressure. Existing job limits bound retained work: three active
jobs per account and 10,000 selected recipients per job. A hold preserves the
receipt, payload and captured consumers, without extending delivery-age limits,
execution history, invocation retention or deduplication guarantees. Execution
recovery still requires its parent execution records; a held receipt alone does
not guarantee replay eligibility or handler success.

`retention` samples label primary recovery holds `recovery_pending`. Preflight's
`receipt_retention_held_count` includes current backfill and recovery holds, and
`receipt_protection_until` identifies this job's opt-in deadline while active.
This deadline protects pending receipts only; preflight acquires no new holds.
Storage utilization alerts remain account-wide.

Apply the migration and upgrade every scheduler/pruning worker before enabling
this option in API binaries. Pruning now acquires the account range lock before
taking the deletion statement's snapshot; a hold committed before pruning is
visible to that statement. Downgrade binaries and complete or cancel protected
active jobs before rolling back the migration. See
[ADR-831](adr/831-recovery-receipt-retention-holds.md).

### Pause, resume, or slow a recovery job

If outcomes show renewed failures, pause further admissions without losing the
job's frozen selection:

```sh
gregale events recovery-pause JOB_ID --yes
gregale events recovery-rate JOB_ID --rate 2 --yes
gregale events recovery-resume JOB_ID --yes
gregale events recovery-status JOB_ID
```

These controls apply to routing and execution recovery. Pause waits for an
in-flight admission transaction, then leaves remaining items pending. Already
queued routing retries and handler executions continue; their execution results
remain observable. Resume continues the same selection and progress. The job
response reports `state: paused`, its current `paused_at`, and the current
`rate_per_second`. The original rate stays in `selection.rate_per_second`.

Rate changes accept 1–100 items per second while running or paused. They preserve
spent permits in the current one-second window and keep existing admission
waits. Lowering the rate can delay new admissions until that window ends;
increasing it does not accelerate an already scheduled wait. Pause/resume never
resets the delivery budget.

Paused jobs still count toward the three-active-job account quota and retain
their original 24-hour expiry. The worker expires paused jobs too, cancelling
remaining items without admitting them. Completed, cancelled, or expired jobs
cannot be paused, resumed, or rate-adjusted and return 409. Repeating a desired
control state on an active job is safe. To abandon a paused job, use
`recovery-cancel JOB_ID --yes`; admitted handlers continue.

API and SDK clients use `POST /v1/event-recoveries/{jobID}/pause`, `POST
/v1/event-recoveries/{jobID}/resume`, and `PUT
/v1/event-recoveries/{jobID}/rate` with `{ "rate_per_second": 2 }`. Writes require
deploy-write/admin scope and MFA. See [ADR-808](adr/808-recovery-job-controls.md).

Discover paused jobs and recent recovery runs without retaining their IDs:

```sh
gregale events recovery-list APP --state paused
gregale events recovery-list APP --mode execution --subscription-id SUBSCRIPTION_ID --limit 10 --json
gregale events recovery-list APP --created-after 2026-10-01T00:00:00Z
```

`recovery-list` returns newest jobs first, showing admission progress, current
rate, pause time and expiry. Execution outcomes remain available with
`recovery-status JOB_ID`. Use `--cursor NEXT_CURSOR` with the same app and filters
to continue; `--limit` accepts 1–50 and may change between pages. Creation bounds
are exclusive RFC3339 timestamps. Subscription filtering matches the original
selection filter or any frozen job item. Changing filters starts a new search.

The API is `GET /v1/apps/{slug}/event-recoveries`, requiring apps-read/admin scope
and MFA. SDK clients provide Go `ListEventRecoveries`, Node
`EventsService.listEventRecoveries`, and Python
`faas_sdk.api.events.list_event_recoveries`. Pages include `jobs` and, when more jobs exist,
`next_cursor`. State changes and retention remain live between pages; stored
active states can remain visible until the worker processes expiry. See
[ADR-809](adr/809-recovery-job-discovery.md).

Recovery control history identifies the authenticated account or API key that
created or changed a job. Add an optional reason to write controls:

```sh
gregale events recovery-create APP --mode execution --reason "Recover after billing fix" --yes
gregale events recovery-pause JOB_ID --reason "Consumer failures increased" --yes
gregale events recovery-rate JOB_ID --rate 2 --reason "Reduce downstream load" --yes
gregale events recovery-resume JOB_ID --reason "Downstream service recovered" --yes
gregale events recovery-cancel JOB_ID --reason "Abandon remaining recovery" --yes
gregale events recovery-history JOB_ID --limit 20 --json
```

History records creation, effective pause/resume/rate changes, cancellation and
automatic expiry. Entries include actor, timestamp, previous/new state and rate,
and optional reason. API key identity identifies the credential rather than an
individual person. Expiry uses a system identity. Repeating a control that
changes nothing adds no entry. The history write commits with its action; expiry
cleanup detected by a control can commit an expiry entry before returning 409.
Natural completion and handler attempts are observed through recovery status and
items, rather than this control trail.

Use `--after NEXT_AFTER` for subsequent oldest-first pages. The API is
`GET /v1/event-recoveries/{jobID}/history?after=0&limit=20`, requiring apps-read/admin
scope and MFA. SDK methods are Go `ListEventRecoveryHistory`, Node
`EventsService.listEventRecoveryHistory`, and Python
`faas_sdk.api.events.list_event_recovery_history`. History is retained with the
job; older actions from before this feature are not reconstructed.

Creation/rate bodies accept `reason`; pause/resume/cancel accept an optional
`{ "reason": "Downstream service recovered" }` body. Empty bodies remain supported.
Reasons must be valid UTF-8, at most 512 bytes and contain no control characters.
They are stored in history, not the frozen recovery selection. Go retains the
existing body-free control methods and adds `PauseEventRecoveryWithReason`,
`ResumeEventRecoveryWithReason`, and `CancelEventRecoveryWithReason`.
See [ADR-810](adr/810-recovery-control-audit-history.md).

Inspect active recovery progress and expiry risk:

```sh
gregale events recovery-health APP
gregale events recovery-health APP --json
```

The health view reports running, paused, stalled and expiring job counts, with
paused expiry risk shown separately. Each job includes pending work, rate, last
tracked progress, next eligible attempt and expiry. Progress advances only when
an item is admitted or skipped; controls and retries that leave an item pending
do not advance it. If tracking is unavailable, `progress_known` is false and the
age uses job creation as an explicit fallback.

A job is stalled after at least five minutes without progress and five minutes
overdue for its next eligible attempt. Eligibility accounts for rate limits,
capacity retry delays and legacy receipt claims. Ongoing capacity and claim
retries appear as explicit waits, rather than scheduler stalls. Resume schedules
a fresh eligibility grace without rewriting progress. Paused jobs appear
separately and do not contribute to stalled or running-expiry alert counts.

Pending work within an hour of expiry, or already overdue for expiry cleanup,
is flagged as expiring. Paused expiry risk remains visible separately. Terminal
jobs are omitted from health; use recovery-list or recovery-status for them.
Health describes recovery admission, while handler outcomes remain on status
and items.

The API is `GET /v1/apps/{slug}/event-recoveries/health`, requiring apps-read/admin
scope and MFA. SDK clients expose Go `GetEventRecoveryHealth`, Node
`EventsService.getEventRecoveryHealth`, and Python
`faas_sdk.api.events.get_event_recovery_health`.

Existing alert rules support app-scoped `event_recovery_stalled_jobs` and
`event_recovery_expiring_jobs`. Configure a threshold greater than zero to notice
at least one affected running job. These are current counts; the rule window
does not aggregate history. Rules use supported 5m/15m/1h/6h/24h windows, omit
`event_subscription_id`, and permit webhook notifications only. Existing cooldown
and recovery notifications apply. Read failures produce a degraded observation,
not a healthy zero. No rules or notifications are created automatically.
See [ADR-811](adr/811-recovery-health-and-alerts.md).

Receive terminal recovery notifications through app webhooks:

```sh
gregale webhooks add --app APP --target-url https://receiver.example.com/recoveries \
  --event event_recovery.completed \
  --event event_recovery.cancelled \
  --event event_recovery.expired \
  --retry-policy default
```

`event_recovery.completed` means the frozen selection has finished admission,
including an empty selection created already completed. It does not mean the
admitted handlers succeeded. `event_recovery.cancelled` reports an effective
operator cancellation; `event_recovery.expired` reports automatic expiry, even
when cleanup is detected by a control returning 409. Both cancellation outcomes
retain job state `cancelled`. Previously admitted work continues.

Each event contains a stable `event_id`, job/app IDs, recovery mode, stored state,
outcome, final selected/pending/queued/skipped/cancelled counts, and
creation/expiry/completion timestamps. Pending is zero. Payloads omit application
data, errors, operator reasons and live execution outcomes. Use recovery status
or items to observe handler results.

The outbox write commits with the terminal transition. Recipients are matching,
enabled app-scoped webhooks captured at that moment. An empty app event filter
includes these lifecycle events; account release and platform tenant receivers
do not receive them. Later subscriptions do not receive past transitions, and
repeating a terminal control creates no new notification.

Existing webhook signing, JSON/CloudEvents formats, independent retry policies,
delivery attempts, dead letters and operator retry apply. JSON deliveries carry
the recovery payload under `payload.data`; CloudEvents carries it under `data`.
Payload `event_id` is stable across receivers and retries, while each receiver has
its own delivery ID. Notifications are at least once and may arrive out of order.
Deduplicate business effects by event ID within the receiving consumer before
acknowledging them. One failed receiver does not block recovery completion or
another receiver's notification delivery.

Go exposes `EventRecoveryFinishedWebhookPayload`; Node and Python SDKs generate
the corresponding payload model and updated webhook filter enums. No new
recovery-specific subscription commands are needed. See
[ADR-812](adr/812-recovery-lifecycle-notifications.md) and
[receiver verification](webhook-receiver-verification.md).

Recovery health now includes capacity diagnostics for execution recovery:

```sh
gregale events recovery-health APP
gregale events recovery-health APP --json
```

When admission hits a pending-delivery limit, `capacity_wait` identifies account,
app or captured consumer scope and supplies a safe explanation, episode start,
last observation and age. Untyped capacity exhaustion uses `unknown`; execution
lane locks are not reported as pending-delivery limits. No application payloads,
resolved work keys or raw errors appear in diagnostics. `next_attempt_at` shows
the retry schedule, while `eligible_at` also accounts for the rate budget.

The current item remains pending. Repeated capacity retries retain the episode
start and refresh the observed scope/time. Admission or skip clears the episode;
effective resume starts fresh tracking so intentional pause time does not count
as contention. Paused jobs retain their prior observation for inspection and are
excluded from capacity counts. Older waits acquire diagnostics on their next
observed retry rather than receiving guessed historical timestamps.

`capacity_waiting_jobs` counts fresh running capacity waits.
`prolonged_capacity_wait_jobs` counts those lasting at least fifteen minutes,
with a capacity observation within the five-minute freshness grace. Stalled,
expired and paused jobs are excluded. Diagnostics show the last observed gate,
so a stale observation does not prove a limit is still full.

The existing alert system supports `event_recovery_capacity_wait_jobs`, observing
the prolonged count. Use an app-scoped webhook rule with `gt` zero, no subscription
selector, and a supported 5m/15m/1h/6h/24h window. Existing cooldown and recovery
behavior apply; read failures degrade the observation rather than produce a
healthy zero. Scheduler stalls and expiry risks remain separate metrics. See
[ADR-813](adr/813-recovery-capacity-diagnostics.md).

Assess a frozen recovery job before resuming it:

```sh
gregale events recovery-preflight JOB_ID
gregale events recovery-preflight JOB_ID --json
```

Preflight is read-only. It reports all pending items as currently eligible,
waiting, likely skipped, or unknown, and includes up to 100 sample positions for
inspection through recovery-items. Checks cover retained receipts, captured
recipients, frozen execution/failure identity, replay lineage, absolute delivery
and work deadlines, target availability, and current execution admission capacity.
Reasons include changed executions, expired work or receipts, unavailable targets,
legacy receipt claims and capacity scope. Unknown cases remain explicit.

Capacity observations evaluate candidates independently against current
account/app/consumer pending-delivery limits. They do not reserve slots or prove
that the whole selection can be admitted together. Samples do not expose payloads,
raw errors, resolved work keys or tenant/consumer identifiers.

The report compares remaining lifetime with an optimistic rate-window drain
estimate. It respects the configured rate, spent permits and next scheduled
attempt, includes likely skips, and assumes immediate resume for paused jobs.
An available first-window burst can have a zero-second minimum. The estimate
excludes future capacity waits, worker latency, handler execution and subsequent
changes. `fits_before_expiry: true` is not a completion guarantee. Terminal and
expired jobs have `active: false` and cannot be made active by preflight.

The API is `GET /v1/event-recoveries/{jobID}/preflight`, requiring apps-read/admin
scope and MFA. SDK clients expose Go `GetEventRecoveryPreflight`, Node
`EventsService.getEventRecoveryPreflight`, and Python
`faas_sdk.api.events.get_event_recovery_preflight`. No progress, pacing, audit,
expiry or notification state changes during the read. See
[ADR-814](adr/814-recovery-preflight.md).

### Recovery execution completion notifications

Subscribe separately when you need to know that admitted recovery handlers
have reached confirmed terminal outcomes:

```bash
gregale webhooks add --app APP --target-url https://example.com/hooks/recovery \
  --event event_recovery.execution_finished --retry-policy default
gregale events recovery-status JOB_ID --json
```

For execution recovery jobs created after this migration, the scheduler captures
`event_recovery.execution_finished` after admission finishes and every queued
item has an exact saved terminal result. The original
`event_recovery.completed` still means admission completion. Cancellation or
expiry of admission does not stop handlers already queued; their final results
can later produce the execution event. Routing recovery, historical jobs and
jobs with no queued items do not produce it. Unknown, uncertain, running or
retrying results block notification; they are never counted as success.

The metadata-only payload includes admission counts, an `execution` summary,
`unresolved_count=0` and `execution_finished_at`. `outcome=all_succeeded` means
all queued executions succeeded; admission skips/cancellations remain separate.
`finished_with_non_success` means at least one queued execution failed, reached
a dead letter, expired, was cancelled or was superseded. Summary
`tracked_count` and `saved_results` equal the admission `queued_count`.
`completed_at` is admission completion; `execution_finished_at` is the scheduler
capture time, also shown in recovery status and list responses and status CLI
output. It does not prove webhook acknowledgement.

The scheduler checks a bounded batch of due jobs and defers unresolved jobs for
ten seconds. Capture and webhook recipient selection commit atomically. Even
when no receiver matches, capture is final: adding a receiver later does not
backfill this event. Job retention remains thirty days from admission completion
or cancellation; if evidence is unresolved until pruning, no completion event is
promised. Read-only status calls do not capture notifications.

JSON delivery nests the payload under `payload.data`; CloudEvents uses `data`.
The UUIDv5 `event_id` is stable across receivers and retries. Verify signatures
and deduplicate effects before acknowledgement. Each receiver has independent
retries and dead-letter recovery. Admission and execution notifications have no
relative delivery-order guarantee. Existing wildcard app webhook receivers also
receive this event for eligible jobs.

Apply the migration before API and scheduler upgrades. Update SDK consumers
that strictly validate webhook event names to accept
`event_recovery.execution_finished` first. Go exposes
`EventRecoveryExecutionFinishedWebhookPayload`; Node and Python generate the
same model. See [ADR-833](adr/833-recovery-execution-completion-notifications.md).
