# Event operations

How to watch and repair event delivery once it is running: consumer
execution health, bulk and paced recovery, recovery notifications, producer
keys, transactional consumer writes, subscription capacity commands and
deadline alerts. Publishing, subscriptions, ordering, retries and the
dead-letter queue are in [Events](events.md).

## Consumer execution health

Routing health ends at admission. Inspect what admitted handlers do separately:

```sh
gregale event subscription-execution-health my-app <subscription-id> --window 15m
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

For example, use `gregale alert create` with the app and webhook flags,
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
gregale event recovery-preview invoice-worker --mode execution \
  --subscription-id SUBSCRIPTION_ID --event-type invoice.created \
  --outcome dead_letter --min-age 10m

gregale event recovery create invoice-worker --mode execution \
  --subscription-id SUBSCRIPTION_ID --event-type invoice.created \
  --outcome dead_letter --min-age 10m --rate 5 --yes

gregale event recovery items JOB_ID
gregale event recovery cancel JOB_ID --yes
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
gregale event recovery get JOB_ID
gregale event recovery items JOB_ID --limit 100
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
gregale event recovery get JOB_ID
gregale event recovery items JOB_ID --limit 100 --json
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
[ADR-914](adr/914-durable-recovery-terminal-results.md).

### Protect receipts during bulk recovery

Opt in when creating a routing or execution recovery job:

```sh
gregale event recovery-preview APP --protect-receipts
gregale event recovery create APP --protect-receipts --rate 2 --yes
gregale event recovery-preflight JOB_ID
gregale event retention --app APP
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
[ADR-913](adr/913-recovery-receipt-retention-holds.md).

### Pause, resume, or slow a recovery job

If outcomes show renewed failures, pause further admissions without losing the
job's frozen selection:

```sh
gregale event recovery pause JOB_ID --yes
gregale event recovery update JOB_ID --rate 2 --yes
gregale event recovery resume JOB_ID --yes
gregale event recovery get JOB_ID
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

If processing a job's next item fails, the job waits before trying again,
from 5 seconds doubling up to an hour, and other jobs keep running. After ten
failures in a row the job is cancelled; its history shows a `cancelled` entry
by the system actor `recovery_failure` whose reason names the class of the
last failure.

API and SDK clients use `POST /v1/event-recoveries/{jobID}/pause`, `POST
/v1/event-recoveries/{jobID}/resume`, and `PUT
/v1/event-recoveries/{jobID}/rate` with `{ "rate_per_second": 2 }`. Writes require
deploy-write/admin scope and MFA. See [ADR-808](adr/808-recovery-job-controls.md).

Discover paused jobs and recent recovery runs without retaining their IDs:

```sh
gregale event recovery list APP --state paused
gregale event recovery list APP --mode execution --subscription-id SUBSCRIPTION_ID --limit 10 --json
gregale event recovery list APP --created-after 2026-10-01T00:00:00Z
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
`faas_sdk.preview.api.events.list_event_recoveries`. Pages include `jobs` and, when more jobs exist,
`next_cursor`. State changes and retention remain live between pages; stored
active states can remain visible until the worker processes expiry. See
[ADR-809](adr/809-recovery-job-discovery.md).

Recovery control history identifies the authenticated account or API key that
created or changed a job. Add an optional reason to write controls:

```sh
gregale event recovery create APP --mode execution --reason "Recover after billing fix" --yes
gregale event recovery pause JOB_ID --reason "Consumer failures increased" --yes
gregale event recovery update JOB_ID --rate 2 --reason "Reduce downstream load" --yes
gregale event recovery resume JOB_ID --reason "Downstream service recovered" --yes
gregale event recovery cancel JOB_ID --reason "Abandon remaining recovery" --yes
gregale event recovery history JOB_ID --limit 20 --json
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
`faas_sdk.preview.api.events.list_event_recovery_history`. History is retained with the
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
gregale event recovery health APP
gregale event recovery health APP --json
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
jobs are omitted from the admission section; use recovery-list or recovery-status
for their full details. The separate execution section describes unresolved
handlers after admission finishes.

The `execution` section inspects the oldest 50 retained execution jobs with
completed/cancelled admission and missing exact saved replay results. It reports
waiting jobs, waits lasting at least 15 minutes since admission completion,
unknown evidence, and jobs within 24 hours of their nominal 30-day retention
boundary. These counts overlap. A prolonged wait does not imply handler failure.
Up to three oldest job diagnostics show queued/running/retrying handlers,
untracked replay identities, unknown evidence, missing saved results and known
terminal observations still awaiting saved confirmation. Historical jobs remain
visible even when they cannot emit a new notification; `notification_pending`
describes capture eligibility separately. Reads never capture results or send
notifications. Fully saved and zero-admission jobs are excluded.

`counts_complete=false` means the counts are lower bounds from the oldest 50
jobs. Metadata selection may scan other retained items; the entire read has a
five-second budget. A timeout yields a failed health read, not healthy counts.
The retention boundary is nominal; cleanup may occur later.

Four app-scoped, webhook-only alert metrics use this execution section:
`event_recovery_execution_waiting_jobs`,
`event_recovery_execution_prolonged_wait_jobs`,
`event_recovery_execution_unknown_jobs`, and
`event_recovery_execution_retention_risk_jobs`. Existing windows control evaluation
cadence for these snapshot gauges. Partial counts can fire satisfied `gt`/`gte`
thresholds, but cannot clear an alert; other partial comparisons degrade. Older
servers lacking the section and failed reads also degrade. Configure rules using
the existing alert tools; none are created automatically. See
[ADR-917](adr/917-execution-recovery-health-alerts.md) for rollout and downgrade.

The API is `GET /v1/apps/{slug}/event-recoveries/health`, requiring apps-read/admin
scope and MFA. SDK clients expose Go `GetEventRecoveryHealth`, Node
`EventsService.getEventRecoveryHealth`, and Python
`faas_sdk.preview.api.events.get_event_recovery_health`.

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
gregale webhook create --app APP --target-url https://receiver.example.com/recoveries \
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
gregale event recovery health APP
gregale event recovery health APP --json
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
gregale event recovery-preflight JOB_ID
gregale event recovery-preflight JOB_ID --json
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
`faas_sdk.preview.api.events.get_event_recovery_preflight`. No progress, pacing, audit,
expiry or notification state changes during the read. See
[ADR-814](adr/814-recovery-preflight.md).

### Recovery execution completion notifications

Subscribe separately when you need to know that admitted recovery handlers
have reached confirmed terminal outcomes:

```bash
gregale webhook create --app APP --target-url https://example.com/hooks/recovery \
  --event event_recovery.execution_finished --retry-policy default
gregale event recovery get JOB_ID --json
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
same model. See [ADR-915](adr/915-recovery-execution-completion-notifications.md).

### Retry failures from one recovery job

After fixing a consumer, preview only failures from one terminal execution
recovery instead of selecting app-wide failures:

```sh
gregale event recovery-preview APP --parent-job PARENT_JOB_UUID \
  --outcome failed --json
gregale event recovery create APP --parent-job PARENT_JOB_UUID \
  --outcome failed --request-id 95ba3321-7648-4c93-9720-876ea9b44645 \
  --rate 5 --protect-receipts --reason 'Consumer fix deployed' --yes
gregale event recovery items CHILD_JOB_UUID --json
```

Generate and save a fresh request UUID for each intended child creation. Repeat
an uncertain creation with the **same** UUID and selection to return the same
child job, even after its parent is pruned, while the child remains retained.
Changing the app or normalized selection with that UUID returns conflict. Changes
to operator reason do not create another audit entry; the original reason wins.
If you also set an HTTP `Idempotency-Key`, keep the request body identical for its
separate transport-level checks. Do not reuse a request UUID after child pruning. Parent-scoped creation requires
compatible binaries and the migration; a re-upgrade restores request identities
for retained children from their immutable selection.

`--parent-job` implies execution mode; explicit routing is rejected. The existing
API endpoints take `parent_job_id`, explicit `mode=execution`, and a `request_id`
required on creation but optional on preview. The parent must be a retained
completed/cancelled execution job in the same account and app. Its admission must
be terminal; other queued handlers can still be running. Routing or active parents
return conflict; wrong-account/app or pruned parents return not found.

Selection uses only the parent's saved failed or dead-lettered queued executions,
by exact replay invocation, generation and creation time. Omit `--outcome` to
select both; `--outcome dead_letter` selects dead letters. Subscription, event
source/type and minimum-age filters can narrow the set. Success, active, unknown,
cancelled, expired and superseded results are excluded. A new request freezes the
current set; retrying that request does not add failures recorded later. Preview
is advisory and does not reserve a selection or request ID.

A saved failure remains visible even if its execution record or receipt has been
pruned. The worker revalidates it before admission and reports changed,
receipt-expired, or expired evidence as skips. It never substitutes a newer replay
or success, and current uncertain evidence cannot be replayed through this child.
Concurrent recoveries/manual replays use existing one-child and generation guards
so a changed identity is skipped instead of admitted again.

The child's selection identifies its parent; every selected item exposes
`parent_job_id` and `parent_position`, also shown in CLI output. Links remain
historical metadata after parent pruning. Children retain existing quotas, pacing,
receipt protection, preflight, pause/resume/cancel controls, audit history and
execution-finished notifications. Skips are admission outcomes, not handler
success. Replay is still at least once and does not undo work already applied or
restore ordering after newer deliveries have advanced.

Apply the child-request migration before upgrading the API and scheduler. Go,
Node and Python use their existing recovery preview/create methods with the new
fields. See [ADR-916](adr/916-parent-scoped-execution-recovery-retries.md).

### Inspect recovery notification delivery

```bash
gregale event recovery-notifications JOB_ID
gregale event recovery-notifications JOB_ID --json
```

This read-only report separates admission notification capture from execution
notification capture and receiver acknowledgement. Each receiver shows its
current delivery state, attempt count and replay generation, last HTTP response
code, next retry when scheduled, and whether its subscription still exists.
Retained deliveries link to the existing attempt-history API. Dead deliveries
with a retained subscription show the existing independent retry command:

```bash
gregale webhook retry --app APP WEBHOOK_ID DELIVERY_ID
```

Retry still requires the normal write authorization and MFA. Reporting never
retries deliveries, captures notifications, or changes handler execution.

New captures preserve the selected receiver IDs on the job, even when none
match. A retained outbox can supply historical selection evidence. Once only
individual deliveries remain, the report cannot prove which other receivers
were selected: `recipients_known=false`, `counts_complete=false`, and
acknowledgement remains unknown. Missing or pruned delivery evidence also remains
unknown. `awaiting_relay` requires a retained outbox; it does not imply that a
removed receiver will receive a delivery. Current subscriptions are never used
to reconstruct past selection. Receiver availability means that a subscription
exists, not that it is enabled or reachable.

`acknowledgement_status=acknowledged` requires complete known selection and a
succeeded delivery for every selected receiver. An empty known selection is
`no_receivers`. Transport acknowledgement does not prove downstream effects;
receivers must still deduplicate the stable event ID. Admission and execution
notifications have no relative delivery-order guarantee.

The report returns up to 100 receivers per notification (current per-app plan
quotas top out at 25), marks incomplete counts, and shares the five-second recovery
read budget. Receiver snapshots are retained with the job for its existing
30-day retention period. Wrong-account and pruned jobs return not found.

The endpoint is `GET /v1/event-recoveries/{jobID}/notifications`, requiring
apps-read/admin scope and MFA. SDKs expose Go `GetEventRecoveryNotifications`,
Node `EventsService.getEventRecoveryNotifications`, and Python
`faas_sdk.preview.api.events.get_event_recovery_notifications`. Apply migration
`20261009225025213` before API/scheduler upgrades. See
[ADR-918](adr/918-recovery-notification-delivery-report.md) for historical evidence
and downgrade limits.

### Recovery notification delivery health

`gregale event recovery health APP` also reports separate admission and execution
notification counts: overdue, dead, unknown, and no receivers. These count jobs,
not receivers, and may overlap. A dead delivery can also be overdue. Overdue means
at least one known unacknowledged receiver and a known capture timestamp at least
15 minutes old. Missing history does not establish an overdue delivery. A frozen
empty selection is no receivers, rather than acknowledged.

The view observes up to 50 oldest terminal candidate jobs and samples up to three
problem notices. Fully acknowledged saved selections are excluded. The top-level
notification `counts_complete` describes candidate truncation; admission and
execution completeness additionally describe missing evidence. Partial counts
are lower bounds. Historical jobs can remain unknown until retention pruning.
The bound limits report observations, while finding candidates can scan retained
metadata within the existing five-second request budget. Reads do not capture,
relay, retry, or mutate notification state. Use
`gregale event recovery-notifications JOB_ID` for each receiver's evidence.

Eight optional app-scoped webhook alert metrics are available:
`event_recovery_notification_{admission,execution}_{overdue,dead,unknown,no_receivers}_jobs`.
Create rules explicitly, including no-receiver rules when receivers are expected.
These are current retained counts; rule windows do not accumulate notifications.
Partial observations can only trigger satisfied `gt`/`gte` lower bounds and cannot
clear alerts or send recovery notifications. Other partial comparisons degrade.
Apply migration `20261009225025229_event_recovery_notification_health.sql` before
API and evaluator rollout. See [ADR-919](adr/919-recovery-notification-health-alerts.md).

### Selectively retry recovery notification deliveries

Preview receiver eligibility without changing delivery state:

```bash
gregale event recovery-notification-retry-preview JOB_ID --json
```

The preview separates admission and execution receivers. It exposes delivery
identity, current replay generation, eligibility, and a reason when ineligible.
Only retained dead deliveries with an owned enabled receiver and an eligible
plan can be queued. Missing selection evidence remains incomplete; a retained
exact source-event association can still establish one receiver's eligibility.
Preview does not reserve state or expand an explicit selection.

Copy the receivers you intend to retry into a request file:

```json
{
  "request_id": "0b9d4e92-3d3c-4db1-b2a2-51c1a65793d5",
  "targets": [{
    "kind": "admission",
    "webhook_id": "6cce6212-845f-4caa-a346-2014017af756",
    "delivery_id": "ecb80865-7057-49c5-9351-8c6f8e0f93ed",
    "expected_replay_generation": 0
  }]
}
```

Use actual IDs and generations from the preview. Include one receiver or an
explicit set of up to 100 distinct deliveries; there is no implicit retry-all.

```bash
gregale event recovery-notification retry JOB_ID --request-file retry.json
```

Each result is `queued` or `skipped`. The action rechecks source event, receiver
ownership and enablement, plan, dead state, and generation. Active or successful
siblings stay unchanged. A skipped receiver records a reason such as
`not_dead`, `generation_changed`, `receiver_disabled`, `receiver_unavailable`,
`delivery_unavailable`, `delivery_changed`, or `plan_not_allowed`. All decisions
and queued resets commit atomically. Storage errors roll back the whole request.
Queued delivery retains its delivery and source-event IDs, resets attempts,
and increments replay generation; it does not establish acknowledgement.

Preserve the request file when the response is uncertain. Repeating its stable
request ID returns the original decisions, even if a queued delivery fails again
or a skipped receiver becomes eligible. Changed targets under that ID return
409; target order does not matter. Use current evidence and a new request ID for
a later retry. Up to 100 decisions are retained per job, including all-skipped
requests; existing requests remain readable at that limit. Job pruning removes
receipts and makes the endpoint return 404. Delivery remains at least once;
receivers must deduplicate side effects.

The endpoints are `GET /v1/event-recoveries/{jobID}/notifications/retry-preview`
and `POST /v1/event-recoveries/{jobID}/notifications/retry`. Preview requires
read scope or admin and MFA; retry requires deploy write scope or admin and MFA.
Both reject query parameters. Go methods are
`PreviewEventRecoveryNotificationRetry` and `RetryEventRecoveryNotifications`;
Node and Python expose the generated equivalents. The request body limit is
64 KiB. Apply migration `20261009225025256_event_recovery_notification_retries.sql`
before API rollout. See [ADR-920](adr/920-selective-recovery-notification-retries.md).

### Inspect saved notification retry decisions

List saved decisions for a retained recovery job:

```bash
gregale event recovery-notification-retry-history JOB_ID
gregale event recovery-notification-retry-history JOB_ID --json
```

Inspect one request and compare its frozen queued/skipped result with the latest
retained delivery state:

```bash
gregale event recovery-notification-retry-history JOB_ID --request-id REQUEST_ID
```

The summary shows decision time and queued/skipped counts. Detail shows each
original receiver decision and reason alongside current status and replay
generation. `current_status_observed_at` identifies when that read occurred.
A missing or pruned delivery is `unavailable`; it does not alter the saved result.
The endpoints are
`GET /v1/event-recoveries/{jobID}/notification-retry-decisions` and
`GET /v1/event-recoveries/{jobID}/notification-retry-decisions/{requestID}`.
Both require read scope or admin and MFA, reject query parameters, and return
only metadata. History is bounded at the existing 100 decisions per retained
job and disappears when job retention prunes that job. No migration is required.
Go, Node, and Python expose list and detail methods. See
[ADR-921](adr/921-recovery-notification-retry-history.md).

### Notification retry generation outcomes

Request-specific notification retry history also reports `retry_outcome` for the originally queued `replay_generation`: `pending`, `succeeded`, `failed`, or `unknown`. Skipped decisions report `not_applicable`. Retained terminal attempts establish success/failure and `completed_at`; a later delivery generation cannot establish the earlier generation's outcome. Removed evidence reports unknown.

`retained_attempt_count` counts completed attempts from that generation only. `attempt_count_complete` indicates whether the retained sequence is complete for the known outcome; unknown outcomes always report false. Attempts still in flight are excluded. Read these fields using `gregale event recovery-notification-retry-history JOB_ID --request-id REQUEST_ID --json` or the existing request detail API/SDK method.

### Wait for a saved notification retry

```bash
gregale event recovery-notification-retry-history JOB_ID \
  --request-id REQUEST_ID --wait --timeout 5m --json
```

Waiting uses the original queued generations, polls immediately and then every five seconds after a pending response, and defaults to a five-minute deadline. `--wait` requires `--request-id`; `--timeout` requires `--wait` and a positive duration. Each API read has at most five seconds, bounded by the remaining wait deadline. Skipped receivers do not count as delivered.

The command stops on success for all queued targets, the first known failed target, or unknown evidence. A failed/inconclusive receipt can still contain other pending receivers. All-skipped requests are inconclusive. Progress counts go to stderr. JSON stdout contains one receipt with `job_id`, `request_id`, `status`, optional `reason`, `counts`, and optional `last_observation`. Without `--wait`, the existing history response remains unchanged.

| Exit code | Result |
| --- | --- |
| 0 | Every queued retry succeeded; at least one was queued |
| 1 | A receiver failed, a read/protocol error occurred, or arguments were invalid |
| 2 | Evidence is unknown or no receivers were queued |
| 3 | The wait deadline expired |
| 130 | Interrupted |

Timeouts, interrupts, and later read errors preserve the last valid observation and request identity; an initial read failure has no observation. A pruned job/request stops with a read error. Waiting does not cancel or create deliveries. Resume inspection with the same job and request IDs.

### Notification retry history outcome summaries

The retry history list now includes `succeeded_count`, `failed_count`, `pending_count`, and `unknown_count` for each request's originally queued generations. These sum to `queued_count`; skipped targets remain separate. The list's `observed_at` applies to current evidence, while each request's `decided_at` remains its original decision time.

`status` is `failed` if any queued generation failed, otherwise `inconclusive` for unknown evidence or no queued targets, otherwise `pending` while any target remains pending, otherwise `succeeded`. Later retries do not establish earlier generation outcomes. `completed_at` is the latest terminal attempt finish time and appears only when every queued target has a known terminal outcome and at least one target was queued.

`evidence_complete` requires known outcomes and complete retained attempt sequences for every queued target. An intact terminal attempt can prove success or failure even when earlier attempts are missing; completeness remains false in that case. All-skipped requests have complete empty evidence but are inconclusive. Removed deliveries remain unknown. Use `gregale event recovery-notification-retry-history JOB_ID --json` to scan request summaries, then inspect an individual request for receiver details.

### Filter unresolved notification retry requests

```bash
gregale event recovery-notification-retry-history JOB_ID \
  --status failed,inconclusive --json
```

The history list accepts an optional `status` query parameter with distinct comma-separated values from `succeeded`, `failed`, `pending`, and `inconclusive`. Values form a union; omitting the filter returns all retained requests. Empty values, duplicates, unknown statuses, repeated status parameters, malformed encoding, and other query parameters are rejected. CLI `--status` is list-only and cannot be combined with `--request-id` or waiting.

`matched_count` is the number of returned rows. `totals` always covers the full retained history before filtering and contains `request_count`, request counts by status, and `incomplete_evidence_count`. Status counts sum to `request_count`; incomplete evidence can overlap failed, pending, or inconclusive statuses. A no-match response contains an empty `decisions` array while retaining full totals. Counts and statuses share `observed_at`, use originally queued generations, and do not change stored decisions or retention.

Go callers can pass `api.EventRecoveryNotificationRetryHistoryQuery{Status: "failed,inconclusive"}` as the optional third argument to `ListEventRecoveryNotificationRetryHistory`. Existing two-argument calls remain valid; an empty options status omits filtering. Node/Python SDK list methods expose the optional status parameter.

### App-wide notification retry backlog

```bash
gregale event notification-retry-backlog my-app \
  --status failed,pending,inconclusive --page-size 5 --json
```

GET `/v1/apps/{slug}/event-recoveries/notification-retry-backlog` discovers saved retry requests across retained recovery jobs for the owned app. Statuses default to `failed,pending,inconclusive`; an explicit distinct union can also include `succeeded`. Go uses `ListEventRecoveryNotificationRetryBacklog(ctx, app, query)`; Node/Python expose the equivalent generated method.

`page_size` (CLI `--page-size`) limits inspected **jobs**, default five and maximum ten. Jobs without saved retry requests are excluded. Ordering is newest job creation time first, then job ID descending; request summaries within each job retain decision-time ordering. A page can contain up to 1,000 request summaries. Each row includes `job_id`, `job_created_at`, `summary`, `detail_path`, and `retry_preview_path`.

`counts_scope` is always `job_page`. `totals` counts all requests in the scanned jobs before filtering; `matched_count` counts returned rows and `jobs_scanned` counts inspected jobs. These are page counts, not whole-app totals. Empty filtered pages may still have `next_cursor`: pass it as `cursor` (CLI `--cursor`) to continue. The cursor is bound to the account, app, endpoint, and canonical status selection; retain the same filter when continuing.

Every page is a fresh read-only snapshot with its own `observed_at`. Restart without a cursor to discover newer jobs or updated outcomes in previously scanned jobs. Concurrent writes and retention pruning mean summing page totals does not establish a frozen app-wide total. Original queued generations and missing-evidence behavior match job history; reads never retry deliveries.

To inspect or prepare a retry after finding a request:

```bash
gregale event recovery-notification-retry-history JOB_ID --request-id REQUEST_ID --json
gregale event recovery-notification-retry-preview JOB_ID --json
```

### Selected notification retries across recovery jobs

Use the app retry backlog to identify requests, then inspect each job's retry preview to select exact receivers. Create a selection file with canonical nonzero UUIDs and stable request IDs:

```json
{
  "version": 1,
  "app_id": "11111111-1111-4111-8111-111111111111",
  "jobs": [{
    "job_id": "22222222-2222-4222-8222-222222222222",
    "request": {
      "request_id": "33333333-3333-4333-8333-333333333333",
      "targets": [{
        "kind": "execution",
        "webhook_id": "44444444-4444-4444-8444-444444444444",
        "delivery_id": "55555555-5555-4555-8555-555555555555",
        "expected_replay_generation": 0
      }]
    }
  }]
}
```

```sh
gregale event notification-retry-plan --file selection.json --output plan.json
gregale event notification-retry apply --file plan.json > receipt.json
```

Preview writes a new private plan file (never overwrites) and prints JSON containing exact selections, eligibility reasons and observed evidence. Up to ten distinct jobs and 100 explicit receivers per job are supported; selection/plan files are limited to 1 MiB. Every job must belong to the supplied app. No receiver is selected automatically.

Review the plan before applying it. Preview is advisory: eligibility can change, and the server decides each target using its original generation guard. Application preflights all job/app identities before submitting requests sequentially. Each job commits separately; the batch is not atomic. Application always prints a JSON receipt, including without `--json`. `decided` contains original queued/skipped server decisions; queued does not mean delivered.

Exit 2 means a response needs reconciliation; interruption returns 130. The receipt distinguishes `needs_reconciliation` from subsequent `not_attempted` jobs and includes a history command. Any POST error is treated conservatively as uncertain. Preserve the plan even if receipt output fails. Read saved request history or reapply the **same plan** to obtain the original decisions through idempotency; do not change request IDs or generation guards to resolve uncertainty. Pruned receipts cannot prove a request never executed. Use the existing per-request history wait command to observe delivery outcomes after queuing.

These commands compose existing SDK preview, retry and history methods; no additional server or SDK API is required.

### Reconcile a saved notification retry plan

```sh
gregale event notification-retry reconcile --file plan.json > reconciliation.json
gregale event notification-retry reconcile --file plan.json --wait --timeout 5m > reconciliation.json
```

Reconciliation reads each plan request's saved decision and original-generation delivery outcomes. It never submits retries or changes the plan. The command validates the full selected intent and pins each accepted decision while polling. A later retry generation's delivery status cannot establish success for the original generation.

The final JSON receipt contains every job's decision state, outcome status, counts and last valid observation. `missing` means a 404: the decision is not retained or unavailable, so execution remains unknown. Other read errors and changed identity/intent are reported separately. Last observations survive subsequent errors; `observation_current` identifies evidence accepted in the latest round. Aggregate counts include only current validated observations, excluding unobserved requests. Reads are sequential live observations, not an atomic batch snapshot.

Without `--wait`, read once. With `--wait`, poll every five seconds while the aggregate is pending, within an overall deadline (default five minutes) and five-second per-read deadlines. Stop on read errors, known failure, missing or unknown evidence, or all-skipped inconclusive requests. Every job remains visible even if another job determines the aggregate status. Queued decisions alone do not mean delivery succeeded.

Exit codes: 0 for succeeded, 1 for failed/error, 2 for pending/inconclusive, 3 for timeout, and 130 for interruption. The receipt is always JSON, including without `--json`. Retained evidence is not extended; a missing request must not trigger an automatic replacement retry.

### Application-scoped producer keys

Use `POST /v1/apps/{slug}/events:publish` or:

```sh
gregale event publish-app my-app --file event.json --json
```

```json
{"key":"order-123-created","type":"order.created","data":{"order_id":"123"}}
```

The owned application provides a stable namespace. Keys are exact, case-sensitive printable ASCII without spaces (1..256 bytes); the complete request is limited to 1 MiB. Optional `time` and `schemaversion` follow the existing event contract. The server derives `source=app.<canonical app UUID>` and `id=key.<SHA-256 hex of key>`. Configure subscriptions and schemas against this source. Only the app publishes from it: `POST /v1/events:publish`, `:publish-batch` and the platform-tenant route refuse `app.<UUID>` sources in any UUID spelling (400), and an app's runtime may use only its own, so subscribers can trust the source as the app's word. Ordinary account subscriptions can distribute it to several consumers; the producer app does not restrict recipient selection.

The response contains `app_id`, `source`, `duplicate`, and an ordinary `receipt`. Repeating the same app/key with identical normalized type, schema version and JSON data returns the original receipt and accepted_at, without another fanout or storage charge. Changed content returns 409. Concurrent submissions use the same atomic publication path, so only one identity is accepted. Occurrence time and trace metadata preserve the first publication and do not change duplicate comparison. Retained matching requests are recognized before current schema rules, including schemas registered after initial publication.

After a lost response or retryable server error, preserve the application, key and content. Do not generate a replacement key. This endpoint requires MFA and events:publish, deploy:write or admin and uses existing app ownership and rate limits; it does not use the short-lived request-wide Idempotency-Key cache. Acceptance means durable storage, not successful consumer delivery or exactly-once side effects. Receivers must still deduplicate side effects.

Deduplication lasts while the receipt remains retained. Settled receipts become eligible for pruning after the plan's [settled-receipt window](events.md#settled-receipt-retention); unsettled work and retention holds can extend that period. Repeated publication does not refresh retention. After actual pruning, the same key can create a new acceptance and fanout. Renaming the producer app preserves identity, while deleting and recreating an app creates a new UUID namespace. Legacy account/source/id publication of the derived identity addresses the same event; the namespace is not an isolation boundary between authorized producers in the same account.

The CLI always prints JSON. Go exposes `PublishAppEvent`; Node and Python expose the generated `publishAppEvent` / `publish_app_event` methods. No new storage migration is needed. See [ADR-929](adr/929-application-scoped-producer-key-publication.md).

### Reconcile an application producer key without republishing

```sh
gregale event publish-app-status my-app --key order-123-created --json
# Continue the bounded consumer evidence page:
gregale event publish-app-status my-app --key order-123-created --after CURSOR --limit 100 --json
```

`GET /v1/apps/{slug}/events/publish-status?key=KEY` derives the same identity as app-key publishing and only reads retained receipts. It requires apps:read/admin, MFA and existing app ownership and rate limits. The exact original key is required; optional expected_accepted_at pins a saved receipt instant. Repeated, empty, unknown or malformed query parameters are rejected. No publish, claim, replay or retention refresh occurs.

The response includes `app_id`, `source`, `event_id`, `observed_at`, `status` and `receipt_url`. Retained observations also include the original acceptance `receipt` and the existing rich receipt `evidence`, with routing summaries and independently tracked consumer execution/workflow outcomes. It does not compare a proposed payload with stored content.

| Status | Meaning |
| --- | --- |
| `processing` | Acceptance is retained and routing is still active. |
| `accepted` | Acceptance is retained and routing has settled, including possible routing failures or filtered consumers. |
| `unavailable` | No retained receipt is visible; never accepted, concurrent acceptance and pruning cannot be distinguished. |

Neither processing nor accepted proves successful handler execution. Inspect each consumer's evidence; missing execution evidence remains unknown. Legacy unknown membership remains `snapshot_captured=false`. Unavailable must not automatically trigger a replacement publish, because publication after pruning can create another fanout.

Recipient pages default to 100 rows and allow up to 200. Follow `evidence.next_after` using `after`. Global routing summaries cover retained consumers beyond the returned page; execution rows cover only that page. Pages are live snapshots and cursors bind the original acceptance identity. If pruning/republication makes a cursor stale, restart observation without inferring continuity of the prior acceptance. Each request has a five-second deadline and no-store response.

The CLI always prints JSON and exits 2 for unavailable, 0 for retained acceptance, and nonzero for read/validation errors. Exit 0 is not a delivery-success assertion. Go uses `GetAppEventPublishStatus`, Node `getAppEventPublishStatus`, and Python `get_app_event_publish_status`. The standalone Go SDK preserves recipient rows as `json.RawMessage`; the root Go SDK and generated SDKs use existing typed receipt models. See [ADR-930](adr/930-application-producer-key-publication-status.md).

### Verify original publication content without publishing

```sh
gregale event publish-app-verify my-app --file event.json --json
```

`POST /v1/apps/{slug}/events/verify-publication` accepts the same original JSON body as app-key publishing, including its exact key, type, data and optional schema version/time. Despite POST, it only reads: apps:read/admin, MFA, app ownership and rate limits apply. It accepts only the optional expected_accepted_at query guard and uses the existing 1 MiB body limit and five-second deadline.

Verification compares normalized type, schema version and semantic JSON data using the same retained-identity comparison as publication. Object formatting/order does not make otherwise equal JSON conflict. Occurrence time and trace metadata are excluded. Today's schema registration/admission rules do not invalidate comparison with previously accepted content.

| Status | Meaning | CLI exit |
| --- | --- | --- |
| `match` | Retained content matches the supplied publication intent. | 0 |
| `conflict` | The same app/key identifies different retained type, schema version or data. | 1 |
| `unavailable` | No retained acceptance is visible; nonpublication is not proven. | 2 |

Match and conflict both include the original retained acceptance receipt, read in the same comparison snapshot. No supplied or stored event data is returned. Other read failures remain errors, not unavailable. None of these results automatically submits an event or refreshes retention. A match does not prove handler execution or side effects; use publish-app-status and its consumer evidence separately. A reaccepted key after pruning can describe a newer acceptance, so compare known acceptance timestamps when assessing continuity.

Go exposes `VerifyAppEventPublication`; Node and Python expose `verifyAppEventPublication` / `verify_app_event_publication`. The CLI always emits JSON. No migration is required. See [ADR-931](adr/931-app-publication-content-verification.md).

### Pin reconciliation to a saved acceptance

```sh
gregale event publish-app-status my-app --key order-123-created --expected-accepted-at '2026-10-09T12:34:56.123456Z' --json
gregale event publish-app-verify my-app --file event.json --expected-accepted-at '2026-10-09T12:34:56.123456Z' --json
```

Copy the exact `accepted_at` from the saved receipt, including its fractional digits. Both existing read endpoints accept optional `expected_accepted_at` in the query string. Verification still takes the original publish body. Timestamps must be nonzero RFC3339 with up to nanosecond precision; offset spellings compare by the exact UTC instant. No rounding or tolerance is applied.

When guarded, responses include the normalized expected timestamp and an independent `acceptance` result:

| Acceptance | Meaning |
| --- | --- |
| `same_acceptance` | Current retained accepted_at equals the saved instant. |
| `replacement_acceptance` | The same app/key has a different retained acceptance timestamp. |
| `unavailable` | No retained acceptance is visible; the original's outcome remains unknown. |

Content/routing `status` remains separate. A response can contain `status=match` and `acceptance=replacement_acceptance`: the content matches a newer acceptance. Returned receipt and consumer evidence describe that current acceptance and must not be used as proof of the original's outcome. Replacement returns CLI exit 3; unavailable returns 2; otherwise the existing command exits apply. Unguarded responses omit both new fields and preserve earlier behavior.

Go status queries accept `ExpectedAcceptedAt`; verification methods accept one optional `AppEventAcceptanceGuard`. Node/Python methods expose `expectedAcceptedAt` / `expected_accepted_at`. The guard remains read-only, does not refresh retention, and requires a saved timestamp. It is a timestamp comparison rather than a permanent unique identity token; identical timestamp collisions cannot be distinguished. A stale recipient cursor still fails validation; restart with the saved acceptance guard to inspect the current retained record. See [ADR-932](adr/932-app-publication-acceptance-guards.md).


## Transactional event consumer writes (Go, Node and Python SDKs)

Application subscription consumers can commit a deduplication receipt and their
PostgreSQL business writes together with `WithEventConsumerTransaction`. A lost
response after commit is recovered by returning the saved result on redelivery.
Consumer and acceptance identity keep independent recipients separate, and a new
acceptance after producer-key reuse is distinct. Existing deliveries without the
new acceptance header cannot use the helper.

See [transactional event consumers](event-consumer-transactions.md) for setup,
identity framing, replay behavior, application-owned retention, and the limits
of the database transaction guarantee, including Node and Python handler examples.

### Publish application events from a business transaction

A [version 3 Commit event destination](gregale-commit.md#version-3-application-event-publication-adr-935)
observes an application-owned transactional outbox and publishes under its bound
application's event source. Gregale atomically records publication acceptance,
the subscriber snapshot and the retained Commit receipt. Each subscriber recovers
independently. Relay retries after event retention return the original Commit
receipt rather than publishing again. This destination remains under the existing
Commit qualification gates.

### Limit one subscription's live delivery backlog

```sh
gregale event subscription-capacity set APP SUB --max-pending 16 --yes
gregale event subscription get APP SUB
gregale event subscription-capacity set APP SUB --max-pending 0 --yes
```

`PUT /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity`
accepts `{ "max_pending_deliveries": 16 }`. The field is required and non-null;
zero restores the current plan default. Positive values must fit the current plan's
per-consumer ceiling (Free 64, Hobby 256, Pro 1024, Scale 4096). The operation requires
deploy-write/admin scope and MFA. It changes capacity independently of pause,
resume pacing and circuit controls. Controls survive subscription removal.

This ceiling counts trusted pending plus dispatching invocations, including replay
children. It does not limit publication, routing leases, or running handlers
separately. Lowering below current usage leaves existing deliveries intact and
blocks new admission until usage drains below the effective ceiling. Plan changes
clamp the effective value; zero follows the current plan, while a saved positive
value remains configured. App/account limits remain shared gates, so configuring
one consumer does not reserve capacity for another.

Status exposes `max_pending_deliveries`, `effective_max_pending_deliveries` and
`live_deliveries`. Routing pending/processing counts describe a different stage;
legacy invocations without trusted slots are excluded from live delivery counts.
Reads are observations, not admission reservations. Go, Node and Python SDKs expose
`SetEventSubscriptionCapacity` / generated `setEventSubscriptionCapacity` operations.

Configured capacity uses existing durable `capacity_consumer` waits and health
metrics. A wait spends no routing-failure budget; five-second polling retries when
capacity drains. Delivery age limits still apply. Routing and replay share the
ceiling, live-to-live retries retain slots, and keep-latest/cancellation semantics
remain unchanged. Fair account/consumer scheduling preferences and captured lane
ordering still apply. There is no global FIFO or exactly-once execution guarantee;
consumers retain their normal application-level deduplication.

Apply migration `20261013210000001_subscription_delivery_capacity.sql` and upgrade
all routing/replay writers before relying on custom ceilings. Older routing writers
only enforce plan limits. Downgrade restores the earlier replay guard and plan
limits, discarding overrides. This implementation does not execute the migration.
See [ADR-1174](adr/1174-configurable-subscription-delivery-capacity.md).

### Diagnose subscription capacity pressure

```sh
gregale --json events subscription-delivery-health APP SUB
```

The existing delivery-health response adds optional `capacity_pressure`, containing
its own `observed_at`, complete aggregate coverage, configured subscription ceiling,
and `blocking_scope`. This blocker reflects current trusted live slot counts,
checking consumer, app, then account limits. An empty blocker means no full ceiling
was observed. It does not establish eligibility past ordering, pause/circuit,
filtering or other delivery gates.

The three `scopes` show current live usage, effective limit, `at_limit`, this
subscription's effective capacity-waiting recipient count, oldest waiting event's
acceptance timestamp/age, and operator advice. Consumer usage covers this
subscription; app/account usage includes sibling deliveries. Consumer limits clamp
the saved setting to the current plan. Trusted live slots include pending and
dispatching invocations and replay descendants; untagged legacy work is excluded.

Waiting counts cover all retained pending application recipients in the snapshot,
using the existing backlog waiting-reason precedence. They are independent of the
bounded pending sample and health history window. Recorded waits can remain after
capacity drains, so they do not imply current saturation. The age is since event
acceptance, not how long a continuous capacity wait has lasted. Empty groups omit
the oldest timestamp and report zero age.

For consumer pressure, inspect its live backlog and review its configured ceiling
within plan limits. For app/account pressure, inspect sibling backlogs and allow
live work to drain; raising only this subscription's limit does not remove those
shared ceilings. Advice is informational and performs no mutation.

Pressure has one consistent read-only snapshot, while routing, execution,
capacity-wait counts and other combined health components remain independent
reads. Compare their timestamps before interpreting differences. These
observations reserve no capacity and guarantee neither immediate admission nor
delivery order. Go/Node/Python SDKs expose the optional pressure models; existing
watch thresholds and at-least-once execution behavior remain unchanged. See
[ADR-1175](adr/1175-subscription-capacity-pressure-diagnostics.md).

### Preview a subscription capacity change

```sh
gregale --json events subscription-capacity-preview APP SUB --max-pending 16
gregale event subscription-capacity-preview APP SUB --max-pending 0 --timeout 5s --max-age 30s
```

This CLI command resolves the app UUID and reads existing delivery health, without
sending controls or writing files. Pressure now exposes
`plan_max_pending_deliveries`, the current plan allowance before applying the saved
subscription setting. Preview requires this field; upgrade older servers before
using it. It rejects missing, incomplete, mismatched, stale, future-dated or
inconsistent pressure snapshots rather than inventing usable capacity. Default
total read timeout is five seconds; max-age defaults to thirty seconds and uses
existing watch bounds (100ms to 24h). Interrupting reads exits 130.

The JSON report includes `read_only`, `advisory`, `valid`, the requested value,
validated `pressure`, and a `projection` for valid proposals. Zero projects the
current plan default. Positive values must fit the observed plan ceiling. Invalid
proposals return `valid: false`, a validation error, no projection, and exit 1.
Valid proposals exit 0 even if their projection would block new admission.

Projection shows the effective consumer limit, available consumer slots, and
`required_consumer_drain` before one additional ordinary delivery could fit. At
exactly the limit, one delivery must drain. Lowering does not evict existing work.
The projected first blocker follows consumer/app/account priority, while
`shared_blocking_scopes` lists every full app/account ceiling. Increasing only the
consumer setting cannot remove these shared limits. Usage counts trusted pending
plus dispatching slots, including replay descendants, as in current diagnostics.

`capacity_only_ready` does not establish full delivery eligibility or write
permission/MFA. Pause, pacing, circuit, ordering, filters and per-recipient
admission exceptions remain separate. Preview reserves nothing; plan and usage
can change before apply. Review the report, then explicitly use
`subscription-capacity APP SUB --max-pending N --yes` if desired. Server validation
still controls admission, with no immediate-delivery, ordering or exactly-once
promise. See [ADR-1176](adr/1176-guarded-subscription-capacity-preview.md).

### Apply a reviewed capacity change with value guards

```sh
gregale --json events subscription-capacity-preview APP SUB --max-pending 16
gregale event subscription-capacity set APP SUB --max-pending 16 --expected-max-pending 0 --expected-plan-max-pending 256 --yes
```

Use the actual saved setting and plan allowance from your preview; the numbers
above are an example. Valid preview JSON includes an `expected` object, and human
output prints both expectation flags. Both flags must be supplied together.
Neither flag keeps legacy apply behavior; malformed/partial expectations fail
locally. Apply still requires --yes and normal write scope/MFA.

The existing capacity PUT accepts:

```json
{
  "max_pending_deliveries": 16,
  "expected": { "max_pending_deliveries": 0, "plan_max_pending_deliveries": 256 }
}
```

The expectation object requires both explicit fields. Compare saved configuration,
including zero, rather than the effective clamped limit. A saved setting above a
downgraded plan is allowed as an expectation, but the new setting must fit the
current plan. PostgreSQL holds the plan row and existing capacity/subscription
locks through the comparison and commit; MemStore checks under its mutex.

Changed expectations return HTTP 409, code `event_subscription_capacity_changed`,
with no committed control change. Refresh and review a preview before retrying;
the CLI never retries without guards or invents new expectations. Go/Node/Python
request models expose the optional guard. Upgrade capacity writers before relying
on it; older strict decoders reject the new request field.

These are current-value guards. An intervening change that returns to the same
values can pass. They do not fence app/subscription reincarnation, reserve live
capacity, compare pause/pacing/circuit state, or guarantee admission/order. Those
controls remain intact, and already admitted work is not evicted. After an uncertain
response, inspect status and review again; a repeat conflict is not proof of success.
Plan changes can occur after commit. See
[ADR-1177](adr/1177-guarded-subscription-capacity-apply.md).

### Recover an uncertain capacity change with a command UUID

```sh
gregale event subscription-capacity set APP SUB --max-pending 16 --expected-max-pending 0 --expected-plan-max-pending 256 --command-id 5c531f54-4f73-4f41-83bb-eb8771993256 --yes
```

Choose and save a fresh nonzero UUID before first use. After a timeout, repeat the
same command UUID, proposed value and original expectations. Do not refresh guards
on a retained repeat. The example values are illustrative; use your reviewed
preview expectations. The existing PUT accepts optional `command_id` alongside
`max_pending_deliveries` and `expected`.

A successful request stores its setting change and acknowledgment atomically. A
known matching repeat returns `capacity_command_receipt` without reapplying,
changing control timestamps, consuming another slot or extending retention. The
response's ordinary fields report current state; the receipt reports the original
requested/previous setting, plan/effective allowance, expectations, applied or
unchanged outcome, recording time and deadline. It remains an acknowledgment even
if later commands or plan changes produced different current state.

Different normalized values or guards for an already retained UUID return 409,
`event_subscription_capacity_command_changed`. New commands still check current
guards and plan ceilings. A subscription can retain 128 successful receipts,
including no-ops; a new UUID at the cap returns 429,
`event_subscription_capacity_command_limit`, without applying. Known repeats and
commands without UUIDs remain available. Rejections do not retain successful
receipts or reserve the UUID. No UUID means legacy behavior without command evidence.

Receipts expire thirty days after first success. Repeats do not extend that period.
Expiry frees budget, and a bounded scheduler cleanup removes expired evidence
without changing settings. Parent deletion ends availability. After expiry or
missing evidence, inspect status and review a fresh command rather than assuming
an old UUID can prove success; an expired unguarded request may apply again.

The CLI verifies the original receipt before reporting acknowledgment and displays
its outcome separately from current settings. An incompatible success response is
unconfirmed. It does not manufacture replacement UUIDs, alter expectations or
retry without guards. Current ownership, write scope and MFA still apply to every
repeat. Go/Node/Python expose the optional request UUID and receipt models.

Apply migration `20261013220000001_subscription_capacity_commands.sql` before
upgrading API/scheduler writers and relying on receipts. Downgrade requires retained
evidence to be pruned first. The implementation does not execute this migration.
See [ADR-1178](adr/1178-idempotent-subscription-capacity-commands.md).

### Inspect a capacity command without applying it

Use the same command UUID with the read-only lookup:

```sh
gregale event subscription-capacity get APP SUBSCRIPTION_ID COMMAND_UUID --timeout 5s
```

The GET endpoint `/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity/commands/{commandID}` requires `apps:read` or `admin` and MFA. Go `GetEventSubscriptionCapacityCommand`, Node `getEventSubscriptionCapacityCommand` and Python `faas_sdk.preview.api.events.get_event_subscription_capacity_command` (`sync` or `asyncio`) return the original receipt. `--json` includes the original proposed value, reviewed expectations, previous setting, outcome, original effective and plan ceilings, recording time and fixed retention deadline. The lookup does not change controls or extend the thirty-day retention window.

A receipt describes the original successful command, even if later commands changed capacity. A 404 means retained evidence is unavailable for the owned target; it does not establish that the command never ran. Inspect current controls separately before deciding whether a new change is needed.

### Discover recent capacity commands

```sh
gregale event subscription-capacity list APP SUBSCRIPTION_ID --outcome any --limit 25
# Continue using next_after from the response and the same scope/filter:
gregale event subscription-capacity list APP SUBSCRIPTION_ID --outcome any --limit 25 --after CURSOR
```

The read-only GET collection at `/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity/commands` requires `apps:read` or `admin` and MFA. Outcome can be `any`, `applied`, or `unchanged`. The page limit defaults to 25 and accepts 1–100; explicit HTTP zero is invalid. Go `ListEventSubscriptionCapacityCommands`, Node `listEventSubscriptionCapacityCommands`, and Python `faas_sdk.preview.api.events.list_event_subscription_capacity_commands` (`sync` or `asyncio`) expose the same listing. `--json` includes scope, normalized filter, observation time, original receipts, `has_more`, and `next_after` when available. The CLI fetches one page per invocation.

Receipts sort newest first by recording time, with command UUID descending as a tie-breaker. Cursors are bound to account, app, subscription and outcome filter; changing page size is allowed. Each page observes current retained evidence, so expiry can remove entries and newer commands above a continuation boundary require refreshing from the first page. An accessible empty history returns an empty array. Recording order does not imply delivery order, and missing evidence does not prove a command never ran. Listing never changes controls or extends receipt retention.

### Check capacity-command receipt slots

`events subscription-status APP SUB` and `events subscription-capacity-preview APP SUB --max-pending N` now show a complete advisory receipt budget: retained receipts out of 128, remaining slots, observation time, whether the budget is full, and the earliest retained receipt expiry. Both applied commands and successful no-ops count. Expired receipts stop consuming quota immediately, even before scheduler cleanup.

The existing delivery-control GET returns optional `capacity_command_budget`; delivery-health pressure includes the same metadata from its own snapshot, with the same observation time as pressure. Go, Node and Python response models expose these fields. Preview JSON includes `new_command_receipt_slot_available` when known and a `receipt_budget_warning` when full or unavailable. Older servers without metadata report unknown receipt availability while preserving the existing capacity proposal and projection. Mutation responses may omit the budget.

A full budget warns that new command UUIDs can be rejected; it does not prevent known unchanged-input command repeats. Commands without UUIDs remain available but have no idempotent receipt evidence. Earliest expiry can free a slot, but concurrent commands can consume it. Status and preview reserve no slots, never prune receipts or extend deadlines, and do not establish write authorization or execution readiness. A valid preview still returns success when the budget is full; inspect the separate receipt-budget diagnostic before deciding to issue a new command.

### Watch for a full capacity-command receipt budget

```sh
gregale event subscription-delivery-health APP SUB --follow --receipt-budget-full-threshold 30s --interval 5s --timeout 10m
```

The optional threshold defaults to zero (disabled) and accepts the existing 100ms–24h duration range. Go SDK users set `EventDeliveryHealthWatchOptions.ReceiptBudgetFull` on `WatchEventConsumerDeliveryHealth`. The metric `capacity_command_receipt_budget_full_seconds` measures locally confirmed fullness: an incident opens only after at least two fresh full observations and the configured elapsed duration. A fresh valid budget with remaining receipt slots resolves it. It does not measure the age of a command or prove continuous fullness between polls.

Missing budget metadata on older servers, incomplete or malformed counts, scope/time mismatches, stale or repeated observations, and future-dated samples mean unknown. An active alert loses confirmation rather than falsely reporting recovery; sample gaps reset confirmation. Resume requires fresh sustained evidence and does not count downtime. Existing `--checkpoint`, `--resume`, `--alerts-json`, and acknowledgment handling apply, including persistence before notification and the usual matching watch-selection requirement. Older checkpoint readers must be upgraded before reading checkpoints containing the new metric. Node and Python can continue reading the budget through their health response models.

The watcher only reads health. A full receipt budget can reject new command UUIDs, while retained commands repeated with their original inputs remain available. Alerts never apply capacity changes, reserve slots, prune receipts or extend retention.

### Warn before receipt slots run out

```sh
gregale event subscription-delivery-health APP SUB --follow --receipt-budget-remaining-threshold 16 --receipt-budget-low-sustain 30s --interval 5s --timeout 10m
```

The remaining-slot threshold defaults to zero (disabled); enabled values are 1–128. A warning opens when remaining slots stay **strictly below** N across at least two fresh observations and the confirmation duration. It clears when a fresh valid budget has N or more remaining slots. Confirmation defaults to 30 seconds and accepts 100ms–24h; selecting `--receipt-budget-low-sustain` requires an enabled remaining threshold. Go users set `EventDeliveryHealthWatchOptions.ReceiptBudgetRemaining` and optional `ReceiptBudgetLowSustain`.

Alerts use metric `capacity_command_receipt_budget_used_slots`, with raw retained receipt usage and converted threshold `128 - N + 1`. For N=16, 113 or more retained receipts means 15 or fewer remaining slots. Alerts also carry `minimum_samples: 2` and `sustain_seconds`. This warning can run alongside `--receipt-budget-full-threshold`; each condition has its own incident. Existing checkpoint, resume and acknowledgment handling apply. Resume must use the same threshold and confirmation duration, and older checkpoint readers need upgrading for the new metric.

Missing, stale, incomplete, repeated, future-dated or scope-mismatched evidence remains unknown; active warnings lose confirmation rather than falsely resolving. Gaps reset confirmation and downtime does not count. Warnings do not reserve receipt slots, predict time to exhaustion, extend retention or change capacity.

### Stabilize receipt-budget warning recovery

```sh
gregale event subscription-delivery-health APP SUB --follow --receipt-budget-remaining-threshold 16 --receipt-budget-low-sustain 30s --receipt-budget-recovery-threshold 24 --receipt-budget-recovery-sustain 30s --interval 5s --timeout 10m
```

This warns below 16 remaining receipt slots and clears only after at least two fresh observations at **24 or more** remaining slots spanning 30 seconds. A confirmed incident remains firing between those boundaries and during pending recovery. Dropping below the recovery boundary resets recovery confirmation. Invalid or unavailable evidence loses confirmation and resets both timers. A resumed incident stays unknown in the middle band until a warning or recovery is freshly confirmed; downtime does not count.

Recovery is opt-in. Set a recovery threshold between the warning threshold and 128, or select only a recovery duration to reuse the warning boundary. Recovery confirmation defaults to 30 seconds when enabled and accepts 100ms–24h. Both recovery options zero preserve immediate clearing at the original warning boundary. Go users set `ReceiptBudgetRecoveryRemaining` and `ReceiptBudgetRecoverySustain` in `EventDeliveryHealthWatchOptions`. The separate full-budget duration condition is unaffected.

Conditions and alerts retain the used-slot metric and add `recovery_threshold` (inclusive maximum used slots) and `recovery_sustain_seconds`. In the example, opening usage is at least 113 and recovery usage is at most 104. A zero maximum used-slot recovery threshold may be omitted from JSON when recovery requires all 128 slots; the positive recovery duration identifies the enabled policy. Checkpoints with recovery enabled use **version 2**, which older readers reject. Watches without recovery keep version 1. Resume requires the same opening and recovery settings; use a new checkpoint when changing policy. Alerts remain advisory and perform no capacity or retention changes.

### Inspect watcher confirmation progress

Add `--confirmation-progress` to a receipt-budget watch to print live text details on stdout. Final human output also shows them. Lifecycle alerts stay on stderr, so `--alerts-json` remains an alert-only JSON stream. Omit the live text flag with `--json`; the final JSON watch result includes `confirmations`, and Go `OnProgress` callbacks receive the same observations.

Each selected receipt-budget condition shows its phase: pending warning, firing, pending recovery, waiting for fresh confirmation after resume, clear, unknown, or blocked by a full alert outbox. Active windows include fresh sample counts, elapsed and required duration, and remaining confirmation seconds. Required sample count zero means no active confirmation window. The elapsed duration ends at the latest accepted observation; it does not advance just because output is read later. Remaining seconds are not a promised recovery time and the next observation can reset confirmation.

The most recent local confirmation loss includes its time and a reason such as stale or future evidence, a repeated or out-of-order observation, unavailable evidence, or a sample gap. This diagnostic history is distinct from the current phase. Unknown evidence never reports a successful recovery. Diagnostics and timer counts are not stored in checkpoint conditions; resume starts with fresh sample count zero and never credits old progress or downtime. Existing version-1/version-2 checkpoint and acknowledgment behavior remains unchanged.

### Inspect an application event execution attempt

Authenticated application event handlers can read dispatcher-authored context:

| Header | Meaning |
| --- | --- |
| `X-Faas-Invocation-Id` | Current execution invocation UUID. A child replay has a new UUID. |
| `X-Faas-Invocation-Source` | `async_invoke` for ordinary application-event work or `replay` for a child replay. |
| `X-Faas-Invocation-Attempt` | Positive execution attempt within the current retry budget; signed-int32 range. |
| `X-Faas-Invocation-Replay-Generation` | Nonnegative signed-int64 retry-budget reset generation. Zero is explicitly sent. |
| `X-Faas-Invocation-Replayed-From` | Immediate parent invocation UUID, when available. |
| `X-Faas-Invocation-Replay-Root` | Replay root invocation UUID, when available. |

Use Go `EventConsumerDeliveryContextFromHTTP(request, originalBody, accountID, appID)`, Node `eventConsumerDeliveryContextFromHeaders(headers, originalBody, accountId, appId)`, or Python `event_consumer_delivery_context_from_headers(headers, original_body, account_id, app_id)`. Each validates the existing stable event identity and body before returning diagnostic counters, invocation source/ID and optional lineage. Node preserves replay generation as a decimal string rather than a floating-point number. Duplicate/empty headers, partial counter context, noncanonical or out-of-range numbers, invalid source/UUIDs and immediate lineage cycles are rejected. Completely absent attempt metadata raises the SDK's unavailable-context error; upgrade scheduler and gateway rather than guessing values.

Execution attempts differ from routing attempts before admission. An ordinary retry retains its invocation ID; an in-place retry-budget reset increases generation. A new child replay has a new invocation ID and can start at generation **zero**, so zero alone does not prove an original delivery. Parent/root IDs can clarify lineage when available; absence is not proof that no replay occurred.

The gateway overrides producer-supplied counter/lineage headers using private dispatch fields and forwards them only for scheduler-marked synthetic work. Public HTTP requests cannot forward these reserved headers to guests. Upgrade gateways before the scheduler for rollout; older components do not guarantee the new context. Headers are still not signatures: authenticate delivery and supply trusted owner scope before using any SDK helper.

Continue deduplicating by the existing owned event acceptance and subscription consumer identity. Keep attempt, invocation ID, generation and lineage out of the business receipt key and input digest; the existing transactional helpers remain unchanged. The new metadata adds no ordering or exactly-once guarantee and does not change retry, replay, capacity or retention policies.

### Deferred application processing

Subscriptions can opt into [deferred acknowledgments](event-deferred-acknowledgments.md).
HTTP 202 then transfers the delivery lease to a durable consumer worker, which
renews and explicitly completes using an app-authorized, attempt-scoped token.
Expiry retries only that consumer and respects a finite attempt budget. Ordinary
subscriptions keep HTTP-success completion. This remains at least once; consumer
receipts and external-effect idempotency remain necessary.

### Transactional application event outbox

The standalone Go SDK can atomically stage publication intent alongside business
writes and recover it after commit. Publication requests may include
`expected_app_id`, a canonical application UUID guard; publication and read-only
content verification return 409 on a mismatch before acceptance/comparison. The
outbox always supplies this guard, preserves the producer key, and bounds retries
to an immutable window shorter than normal acceptance retention. See
[the application outbox guide](event-outbox.md) for setup, recovery and limitations.


### Deadline count alerts from the CLI

`events subscription-delivery-health --watch` supports total upcoming/expired
counts, waiting-reason counts and separate recovery boundaries:

```sh
gregale event subscription-delivery-health APP SUB --follow --deadline-window 5m --deadline-upcoming-threshold 50 --deadline-upcoming-recovery 20 --deadline-expired-threshold 1 --deadline-sustain 1m --deadline-recovery-sustain 2m --confirmation-progress
```

Use repeatable `--deadline-upcoming-reason REASON=N` and
`--deadline-expired-reason REASON=N`, with matching `-reason-recovery` flags for
inclusive recovery counts. Existing checkpoint/resume and JSON alert delivery
apply. See [CLI configuration, validation and recovery semantics](event-subscription-deadline-window.md#cli-watch).


### Expired backlog percentage alerts

```sh
gregale event subscription-delivery-health APP SUB --follow --deadline-window 5m --deadline-expired-ratio-threshold 10 --deadline-expired-ratio-recovery 5 --deadline-expired-ratio-min-recipients 100 --deadline-sustain 1m --deadline-recovery-sustain 2m --confirmation-progress
```

This opens at >=10% expired among at least 100 pending deadline-bearing recipients
and confirms recovery at <=5%. Insufficient cohorts stay unknown. Text progress
shows the observed percentage and raw cohort; JSON retains basis-point units and
raw counts. Existing checkpoint/resume and alert acknowledgments apply. See
[ratio validation and lifecycle semantics](event-deadline-ratio-alerts.md#cli-ratio-watch).

### Investigate a saved deadline alert

```sh
gregale event deadline-alert investigate APP --alert-file ./deadline-alert.json --limit 25
gregale event deadline-alert investigate APP --alert-file ./deadline-alert.json --limit 25 --explain
gregale --json events deadline-alert-investigate APP --alert-file ./deadline-alert.json --limit 25 --cursor 'NEXT_CURSOR'
```

Supply one saved Go/CLI lifecycle alert object. The command selects the current
matching cohort, lists delivery identities/deadlines/waiting reasons and keeps alert
notification time separate from current triage observation. It is read-only, bounded
to one page and does not acknowledge or replay anything. See
[file format, pagination and observation semantics](event-deadline-alert-investigation.md#cli-investigation).

`--explain` adds one acceptance-guarded blocker batch for pages of at most 50 rows,
within the same read budget. It reports ordering blockers, current waiting reasons,
capacity scope, target availability and scheduling metadata with separate diagnostic
timestamps and explicit unavailable/error states. See
[blocker explanations](event-deadline-alert-investigation.md#blocker-explanations).
