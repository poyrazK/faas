# Event-driven workloads

Use asynchronous invokes, jobs, webhooks, and scheduled triggers when work does not need to finish in the request path.

Internal events (publishing, `event_triggers` subscriptions, acknowledgments
and delivery controls) are a preview capability. Until it is released to your
account, a deploy that declares `event_triggers` answers `403
capability_not_released`. Run `gregale previews list` to see whether you can
join it with `gregale feature join <key>`, or contact support for access. Its
documentation is on [events and subscriptions](events.md).

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

To write the flow as ordinary async code instead of a step list, see
[code-first workflows](code-workflows.md).

Deploy the manifest with your application handlers. Gregale starts the workflow
directly; an HTTP cron-to-workflow adapter is unnecessary. `run: generate_report`
invokes the app's `/generate_report` handler via the existing workflow executor.
The [example manifest](../examples/scheduled-workflows/gregale.yaml) provides
this two-step recipe.

Workflow execution is a preview feature and may not be enabled for your
account. Inspect the configuration and latest scheduling outcome with:

```bash
gregale workflow schedules --app APP_SLUG
gregale --json workflows schedules --app APP_SLUG
gregale workflow list --app APP_SLUG
gregale workflow steps RUN_ID
gregale workflow attempts RUN_ID STEP_NAME
```

The corresponding inspection API is
`GET /v1/apps/{slug}/workflows/schedules`. Its JSON envelope contains
`runtime_enabled` and `schedules`; `unavailable_reason` explains a blocking
runtime, account, plan, maintenance, or tenant requirement. `enabled` describes
the trigger configuration. `next_fire_at` is a nominal calendar time and is
omitted for disabled or unavailable schedules. `runtime_enabled` reflects
configuration; it does not prove the scheduler is running. `last_status`
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
`gregale workflow run daily_report --app APP_SLUG --input '{"report":"daily"}'`.

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
`gregale workflow attempts <run_id> <step_name>`; each attempt includes its
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
gregale workflow run order_followup --app APP_SLUG --input '{"order_id":"ord_123"}'
```

Inspect the latest step summary with `gregale workflow steps RUN_ID`. To see
each handler invocation or condition-check poll—including its outcome, HTTP
status, start/finish times, error, and next scheduled attempt—use:

```bash
gregale workflow attempts RUN_ID STEP_NAME
```

The equivalent read-only API is
`GET /v1/workflows/runs/{id}/steps/{step}/attempts`. Attempt history stores
metadata, not request or response bodies; timer and callback waits do not
create executor-attempt records.

For a terminal run with one failed or dead HTTP step, retry that step in place:

```bash
gregale workflow retry RUN_ID STEP_NAME
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

A `wait_for_event` step completes with the earliest event of that name the run
has received, including one that arrived before the step started waiting and
one an earlier step already completed with. Events are facts on the run, not a
queue, so every wait for a name matches the same first event. To wait for a
second, distinct signal (a second approval, say), give it its own event name,
or use a [code-first workflow](code-workflows.md), where each `waitForEvent`
consumes one event of its name in arrival order.

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

Each async invocation attempt, including any wake it triggers, must answer
within 30 seconds (`api.GatewaySynthDispatchTimeout`). A longer attempt is
recorded as a timeout ("the invocation attempt exceeded the 30s dispatch
limit") and retried under the invocation's retry policy, so work that needs
longer belongs in a job.

```bash
gregale invoke --async --payload @payload.json APP_ID
gregale invoke --async --on-success-webhook WEBHOOK_ID --on-failure-webhook DLQ_WEBHOOK_ID APP_ID
gregale job run nightly --tasks 10
gregale cron create --app APP_ID --schedule "0 * * * *" --path /jobs/nightly
gregale cron create --app APP_ID --schedule "*/15 * * * *" --command bin/maintenance --arg=--compact
gregale job create nightly-export --image registry.example/exporter:v1 --schedule "0 3 * * *" --timezone Europe/Istanbul
```

Use a scheduled job when work should run to completion in an isolated job
environment, and an HTTP app cron when the schedule should make a request to
an app route; see [Crons](crons). Command crons, which run a command from
the app's deployment on a schedule, are in preview and documented there too.
Scheduled jobs create one task per occurrence and pick up the job's current
configuration at fire time.

Handlers receive an event id and delivery attempt. Persist that id before applying side effects so retries are idempotent. Set explicit payload limits, timeouts, retry counts, and retention; route poison messages to a dead-letter destination for inspection and replay.

Trigger delivery records (`gregale trigger records`) are kept at least 7 days
after their last dispatch, and trigger dead letters (`gregale trigger dlq`) at
least 30 days, unless a record carries its own retention; after that they may
be deleted. Retry or export dead letters you still need before then.

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
`--async-max-age-seconds` on `gregale edge rule create`.

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
`gregale webhook list --app reports`. Route names are stable per app: later
deploys update a matching manifest-owned route and remove stale manifest-owned
routes. Unmanaged edge rules are never adopted or deleted; an exact route
collision fails deployment with guidance to resolve it first. Omitting
`async_routes` leaves managed routes unchanged, while `async_routes: []`
clears them. In project deploys, only selected workloads are reconciled;
omitted route declarations clear that workload's manifest-owned routes, while
`--only` and `--exclude` workloads remain untouched. `--no-triggers` leaves
existing project routes unchanged. Routes default to `POST`; `PUT`, `PATCH`,
and `DELETE` are also accepted. This declaration is YAML-only.

## Invocation history retention

Every finished invocation is kept for your plan's result retention, counted
from when it finished: Free 1 day, Hobby 7 days, Pro 30 days, Scale 90 days.
This covers async invokes, queue and inbox messages, delayed tasks, cron runs,
event deliveries, async edge routes and inbound webhooks. Async invokes and
delayed tasks can ask for a shorter window with `retention_seconds`
(`--retention` in the CLI).

Failed and dead-lettered invocations are kept for at least 30 days, so you can
inspect and replay them from the dead-letter queue (`gregale dlq list <app>`) on
every plan. After the window, the invocation, its attempt history and its
replay links are deleted: `gregale cron runs`, queue history and invocation
lookups only show work inside the window. Duplicate detection that relies on
a stored invocation, such as inbound webhook receipts and async route
idempotency keys, lasts as long as that invocation is kept.

A failed queue or delayed-task message is one entry in `gregale dlq list <app>`: its
invocation. When a push binding delivered it, the binding's receipt failure
stays in `gregale trigger dlq`, and replaying the entry re-arms that receipt.
See [ADR-20261022203653](adr/20261022203653-one-dead-letter-entry-per-queue-message.md).

## Queue limits

Queues are available on paid plans. These limits apply per plan:

| Limit | Hobby | Pro | Scale |
|---|---|---|---|
| Pending messages per app, across all its queues | 10,000 | 100,000 | 1,000,000 |
| Pending message bytes per account | 100 MiB | 1 GiB | 10 GiB |
| How long a message waits for a worker | 1 day | 4 days | 14 days |
| Message size | 64 KiB | 256 KiB | 1 MiB |
| Delivery attempts before the dead letter | 3 | 10 | 25 |

Messages count as pending from the send until a worker settles them. A message a
worker holds under a lease still counts.

### Queue depth

A send to an app whose pending messages are at its plan's limit fails with
`403 plan_queue_depth`, carrying the limit and the observed count.

### Queue backlog

A send while the account's pending messages hold its plan's byte limit fails
with `403 plan_queue_backlog_bytes`, carrying the limit and the observed bytes.
The limit counts every app of the account. Two sends to different apps at the
same moment may pass it by their own sizes.

### Message expiry

A message that waits longer than its plan allows, counted from its send, moves to
the dead-letter queue with outcome code `queue_message_expired`. Replay it from
there (`gregale dlq list <app>`) once a worker can take it. A message a worker holds
under a lease does not expire.

## Application inbox

For straightforward application-to-application work, send directly to the
target app without operating RabbitMQ, SQS, or NATS:

```bash
gregale event send billing --type invoice.created \
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
gregale webhook create --app checkout-api \
  --target-url https://customer.example/webhook \
  --secret "$WEBHOOK_SECRET"

gregale webhook deliver checkout-api https://customer.example/webhook \
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
Use `--idempotency-key` when retrying a `gregale webhook deliver` call whose outcome
is unknown; a new key can enqueue a second delivery.

## Deployment lifecycle webhooks

Subscribe to `deployment.live` and `deployment.failed` to drive a platform's
customer-facing deployment status without polling:

```bash
gregale webhook create --app checkout-api \
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
gregale webhook create --app checkout-api \
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
`completed_at` or `aborted_at` respectively. When the safe-release circuit
breaker, a person, or the worker lease expiry decided the outcome, the payload
also carries `decision`: the rule and the request, 5xx, probe, latency, OOM and
restart counts for both revisions behind it
([ADR-1447](adr/1447-explained-rollout-decisions.md)). An outcome is enqueued once per
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
