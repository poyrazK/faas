# Recover stored workflow runs

Bring failed, paused and missed stored workflow work back: resume a run
after a terminal failure, recover missed scheduled starts, and set up
recovery plans and policies that do it for you. Watching runs and alerting
on failures is covered in [Run and monitor stored workflows](stored-workflow-operations.md).

## Diagnose and recover a dashboard run

Open a run from **Recent runs** to see its observed queue/wait reason, next wake,
expired worker lease, deployment pin, and step kinds. Select a step to inspect
its attempt states, HTTP statuses and timing. Attempt errors and managed-operation
effect records are excluded from this page. Failed diagnostics or history reads
are reported as unavailable, rather than as an eligible or empty result.

The **Recovery preview** lists steps to reopen, steps to preserve and current
blockers. When eligible, review that list, confirm that reopened steps may perform
real actions again, and choose **Resume failed run**. The existing API rechecks
eligibility, admission requirements and the preview's continuation count.
Concurrent or stale requests cannot admit a second continuation. Repeating the
same submitted form replays its original result through API idempotency.
An unsuccessful action requires refreshing the run and confirming again.

**Recovery history** shows continuation numbers, previous states, reopened step
names and timestamps. It omits historical errors and account identifiers.
Successful results and prior attempts remain retained by the workflow runtime;
resuming keeps the captured definition, inputs and deployment routing rules.

For an active run, expand **Cancel this run**, confirm, and choose **Cancel run**.
Cancellation applies to that run's current state, including a continuation that
may have been admitted since the page was loaded. Completed actions are not
undone, and active work may need time to stop. A completed run is left unchanged
by the existing cancellation API. Cancellation does not require the execution
runtime to be enabled. Resume and cancellation require write permission,
verified email, the app-bound CSRF envelope and a separate action confirmation.
The run's app and stored workflow identity are checked before requesting diagnostics,
history, attempts or a mutation. These dashboard controls do not open native
Customer Workflow Operations admission or establish KVM qualification.

## Resume after a terminal failure

Find runs for an existing stored workflow without knowing a run ID:

```sh
gregale workflow stored runs --app billing --name approval-flow --status failed
gregale workflow stored runs --app billing --name approval-flow --status awaiting_event \
  --created-after 2026-10-01T00:00:00Z --limit 25 --offset 0
gregale workflow stored diagnose --app billing --name approval-flow \
  --run 00000000-0000-4000-8000-000000000001
```

Run history uses the exact stored workflow name and the app's workflow history.
Supported statuses are `pending`, `running`, `awaiting_event`, `succeeded`,
`failed`, and `dead`. Omit `--status` for all statuses, including cancelled
runs as represented by the existing workflow status model. `--created-after`
and `--created-before` accept inclusive RFC3339 timestamps. Page size is 1..100
(default 50), with a nonnegative offset. The JSON report contains app/name,
total matching runs, limit/offset, and metadata summaries; it omits input,
output, tenant identifiers, and raw error text. Human output shows run IDs,
statuses, pinned deployment IDs, and creation times. Use `--status dead` as well
as `--status failed` when investigating terminal failures.

Diagnosis resolves the app and stored workflow, checks the run's app identity and
workflow name, then fetches its read-only diagnostics. A run from another app
or stored workflow is rejected before requesting diagnostics. The JSON report wraps
the diagnostics snapshot with the selected app and name. Human output shows
queue/wait reasons, lease state, step status, resume eligibility and blockers,
and an eligible resume command. This command does not retry, resume or cancel
the run. Both commands require read access to the app, stored workflow, and workflow
history; normal server permissions apply to every request. The stored workflow must
still exist. Historical runs of a deleted stored workflow remain inspectable through
the existing `workflows` commands with appropriate access.

The human `workflow stored health` report includes a run-history command using its
exact returned creation-time window, with fractional seconds preserved. It also
offers `--status failed` and `--status dead` commands when those failures appear
in the report, plus a scoped diagnosis command for the latest failure when its
run ID is valid. Copy these commands to inspect the runs behind the reliability
metrics. Printing health performs no additional history or diagnosis requests;
the commands remain read-only when you execute them. Healthy windows still
include a general history command. Health JSON retains its existing API format.

Inspect a run and preview its continuation before changing it:

```sh
gregale workflow diagnose 00000000-0000-4000-8000-000000000001
gregale --json workflows diagnose 00000000-0000-4000-8000-000000000001
```

`GET /v1/workflows/runs/{id}/diagnostics` returns the same read-only snapshot.
It shows the durable queue reason, future wake, due age, expired worker lease,
original code pin and step status/kind. Step kinds distinguish timer, event,
callback and condition waits. `ready` means dispatch admission is open; it does
not guarantee an available worker or immediate execution.

The `resume` object reports `eligible`, the observed `expected_resume_count`,
sorted `reopened_steps` and `preserved_steps`, and `blockers` with a stable `code`,
fixed `message` and optional `step_name`. Preserved names include successful
actions and branches whose persisted state remains unchanged. A structurally
valid plan can have temporary admission blockers, such as maintenance or a full
active-run quota; its proposed reopened names remain visible.

When the snapshot is eligible and has no blockers, the human CLI output includes
a copyable `gregale workflow resume RUN_ID --expected-resume-count N` command
using the observed resume count. Review the reopened steps before executing it.
Diagnosis remains read-only and does not run the command. Blocked or ineligible
snapshots do not offer a resume command, and JSON retains the existing snapshot
format. The eventual resume request still rechecks eligibility and generation.

| Code | Meaning |
| --- | --- |
| `run_not_failed` | The run is still active or has succeeded |
| `handler_executed` | A failure/timeout handler or its continuation already executed |
| `unsafe_mutation` | An attempted external mutation lacks declared provider idempotency |
| `failed_control_step` | A failed wait or join cannot be reopened |
| `failure_before_dispatch` | No executor attempt exists for the failure |
| `active_step` / `active_attempt` | A call or wait is still active |
| `tenant_unavailable` | The tenant is inactive or its app link was removed |
| `pinned_deployment_unavailable` | The original handler code cannot be served |
| `integration_unavailable` | A current credential or permitted integration binding is unavailable |
| `active_run_quota` | Other active runs fill the app's admission quota (runs waiting on an event count only beyond the plan's parked allowance) |
| `runtime_disabled` | Execution is disabled on the responding API server |

The planner reports its first deterministic blocker plus independent admission
blockers. The snapshot omits inputs, outputs, error text, credentials and tenant
identity; use authorized run/step inspection for payloads and error details.
Reads do not execute actions or reserve capacity. Resume rechecks the gates and
generation, so an eligible preview can become stale before submission. Read-only
API keys can preview; resume still requires write permission. Tenant-bound read
tokens use `GET /v1/platform-tenant-self/workflows/runs/{id}/diagnostics`, restricted
to their own runs and current app links. Both routes use `Cache-Control: no-store`.
Go `GetWorkflowRunDiagnostics`, Node `WorkflowsService.getWorkflowRunDiagnostics`
and Python `workflows.get_workflow_run_diagnostics` expose the account route,
with corresponding tenant-self methods.

After resolving a provider outage or integration configuration issue, inspect
`GET /v1/workflows/runs/{id}` and send its current `resume_count`:

```http
POST /v1/workflows/runs/{id}/resume
Idempotency-Key: invoice-batch-resume-1
Content-Type: application/json

{"expected_resume_count":0}
```

The response is the queued run with `resume_count: 1`. Use the same request
idempotency key to retry this request after a lost response. A new continuation
must send the latest count; concurrent requests with the same count have one
winner. Each run permits up to 16 resumptions and uses the normal active-run
quota. Resume requires a live default deployment, an eligible account/app,
valid integration bindings and the workflow runtime to be enabled.

The CLI exposes the same recovery flow. `workflows status` displays the current
resume count; pass that value to `workflows resume`. Keep the count and key the
same if you repeat a request after a timeout, and use `workflows resumes` to
inspect the continuation audit history:

```sh
gregale workflow status 00000000-0000-4000-8000-000000000001
gregale workflow resume 00000000-0000-4000-8000-000000000001 --expected-resume-count 0 --idempotency-key invoice-batch-resume-1
gregale workflow resumes 00000000-0000-4000-8000-000000000001
```

Successful actions and batch items retain their results. Failed actions receive
a fresh configured retry budget; attempt numbers continue increasing. A failed
batch resumes at its failed item and processes the remaining snapshotted items
in order. The original workflow definition, inputs and guard decisions remain
unchanged. Publishing a new definition does not change this run. App actions
retain the original handler deployment across retries and resumptions. Legacy
unpinned runs retain their existing best-effort routing; diagnostics label them
explicitly. Start a new run to use newly deployed code.

The same logical action Idempotency-Key is reused. App handlers must deduplicate
it across the full retry/resume period. Managed mutations must declare actual
provider idempotency support whose deduplication window covers that period.
GET/HEAD actions may be repeated. Previously attempted mutations without this
support are rejected, even if they returned an HTTP error.

Cancelled runs, active calls or waits, already executed failure/timeout handlers,
failed control steps and failures before dispatch cannot be resumed. This first
version does not override inputs, replace the definition or select an arbitrary
step. Correct a bad definition and start a new run when those changes are needed.

`GET /v1/workflows/runs/{id}/resumes` returns the requesting account, previous
failure, reopened step names, timestamp and resume number. The existing step
attempts endpoint retains all previous attempts. Step inspection exposes
`retry_base`, the attempt number at the last continuation; later attempts consume
the current retry budget. Resume history is retained for as long as the run.

Apply migration `20261003180000001_workflow_resume.sql` and update/drain scheduler
workers before customers use the endpoint. Drain or cancel resumed runs before
downgrading; export resume history before rolling the migration back.

## Recover paused and missed work

A stored workflow that paused itself after repeated failures is resumed with
`gregale workflow stored failure resume`; the pause policy, its preview and
resume are described in
[Automatically pause after repeated failures](stored-workflow-operations.md#automatically-pause-after-repeated-failures).

### Recovering missed scheduled starts

Schedules skip missed fires by default. For reports or reconciliation jobs that
should recover after downtime, opt into the latest eligible missed fire:

```yaml
trigger:
  type: schedule
  schedule: '0 7 * * *'
  timezone: Europe/Istanbul
  catch_up: latest
  catch_up_window: 2h
```

`catch_up` accepts `skip` (the default) or `latest`. The recovery window defaults
to one hour and accepts duration strings between one minute and 24 hours. A
7:00 job observed again at 7:20 starts once with a nominal fire time of 7:00.
If several fires were missed, only the latest one inside the window is selected;
if the current minute is due, it takes precedence. Older missed fires are
discarded. No historical runs start on the first observation or after re-arming
for a deployment, trigger change, tenant cadence update, or stored workflow resume.

Recovery still observes overlap and the shared run quota. A rejected occurrence
is recorded as skipped and consumed, including the older coalesced interval;
freeing a slot does not replay it. History exposes `scheduled_for` and
`evaluated_at` so the original fire and recovery time remain visible. Coalesced
and expired fires are not backfilled into history.

The policy also applies to gaps caused by maintenance, runtime gates, or an
unavailable tenant link. Tenant schedules inherit the owner's recovery policy
while applying their own saved cadence and timezone. Tenants cannot change the
policy or window. `gregale workflow schedules --app reports` and the schedule
inspection APIs show the effective policy and window.

Update validators and scheduler workers before publishing catch-up options.
Remove the options and drain or cancel affected snapshot runs before downgrading
to a runtime that does not understand them.

Existing alert rules accept five notification-only workflow metrics:

| Metric | Observation |
| --- | --- |
| `workflow_failures` | Failed or dead runs completed in the selected window; excludes cancellation |
| `workflow_schedule_quota_skips` | Due schedule occurrences skipped for app quota in the selected window |
| `workflow_pending_age_seconds` | Age of the oldest eligible pending run since it became eligible; excludes future retries; zero if none |
| `workflow_waiting_age_seconds` | Age of the oldest currently awaiting step, zero if none |
| `workflow_due_age_seconds` | Age since the oldest pending run, elapsed parked wake or expired lease became due; excludes future waits and live running claims; zero if none |

Scope an alert to one app or the authenticated account. Age signals describe
current state rather than a window average. Waiting age includes intentional
timers, callbacks, conditions, and event waits, so choose thresholds suitable
for the workflow. These metrics support webhook notifications only.

### Replay a skipped scheduled occurrence

Run a skipped occurrence from [schedule history](stored-workflow-operations.md#admission-history-and-reliability-alerts) with a preview followed by an explicit
replay:

```sh
gregale workflow schedule history replay-preview --app reports \
  --occurrence-id 00000000-0000-4000-8000-000000000001
gregale workflow schedule replay --app reports \
  --occurrence-id 00000000-0000-4000-8000-000000000001
```

Repeat `--occurrence-id` to select up to 20 occurrences; the service processes
them in scheduled order. Preview reports eligibility against the current live
deployment, definition, tenant settings, overlap state, and quota. It reserves
nothing, so replay rechecks all conditions. A replay uses the original nominal
`scheduled_for` time and current trigger input. It is blocked if the deployment
or definition changed, the schedule is disabled, a tenant link is no longer
active, overlap is active, or the app quota is full. History displays the
resulting replay run ID, and retries of the same occurrence return that ID
without creating another run. Legacy history without a definition fingerprint
cannot be replayed.

### Unified recovery preview

Inspect recovery candidates without clearing a pause, admitting events, moving
schedule cursors or resuming runs:

```sh
gregale workflow stored recovery-preview --app billing --name paid-invoice \
  --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z
gregale --json stored workflows recovery-preview --app billing --name paid-invoice \
  --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z --limit 10
```

The report combines current admission intent and failure-pause generation with
separate failed/dead run pages. Each run includes its server-observed status,
resume eligibility, expected resume count, blocker codes, and steps that would
be reopened or preserved. Run inputs, outputs, errors and tenant identities are
omitted. Failed/dead pages cover retained history regardless of creation time;
`--from` and `--until` apply only to event acceptance and schedule evaluation.

For a published event trigger, the report includes captured retained event
metadata, per-event admission reasons, existing admissions, potential admissions,
retention coverage and an
opaque continuation cursor. Potential admission is advisory: pauses, routing,
quotas, retention and current execution gates still apply. A retained-event
count does not guarantee that every event will produce a run.

For a published schedule trigger, it includes the existing schedule policy
preview from `--from` through the evaluation time `--until`. A failure-pause
resume **re-arms without catch-up**, even if the schedule's ordinary policy is
`latest`; the report explicitly labels this distinction. Disabled schedules
may report `disabled` rather than a count of missed fires. Other trigger types
have no retained trigger-work preview, but still show failed/dead run recovery.

The default limit is 10 and the maximum is 25 per run status and event scan
page. The command performs one page request per failed/dead status, one
metadata-only diagnostic request per returned run, and one applicable trigger
preview. Use `--run-offset` for additional run pages and `--event-after` for an
additional event scan page; preserve the same explicit `--from` and `--until`
when continuing events. `--until` defaults to the current time.

All requests use existing authenticated read APIs. Any required read failure
or scope mismatch fails the command instead of emitting a partial report.
`started_at`, `completed_at` and per-run observation times describe the multiple
reads; this is not an atomic snapshot, a complete archive, or a reservation.
Recovery actions recheck their own eligibility and generation fences.

### Controlled recovery of selected runs

Use the run IDs and `expected_resume_count` values from recovery-preview to
resume a bounded explicit selection:

```sh
gregale workflow stored recover --app billing --name paid-invoice \
  --run aaaaaaaa-0000-4000-8000-000000000001:2 \
  --run bbbbbbbb-0000-4000-8000-000000000001:5 \
  --idempotency-key invoice-outage-batch-1
```

Repeat `--run UUID:COUNT` for at most 25 distinct runs. The count is required
and must be the original count reviewed for that run. The batch key is required;
keep it with the selection if the command may need to be retried. The CLI derives
an independent stable request key from the batch key, app identity, stored workflow,
run ID and original count, so reordering the selection preserves those keys.

Before its first write, the command verifies that every selected run belongs to
the chosen app and stored workflow. It then reads each run and its diagnostics again,
checks the original count and failed/dead state, and submits only eligible runs
to the existing resume API. The server rechecks admission and continuation safety
when committing. Completed steps and captured definitions retain the existing
resume semantics; the CLI does not construct a replacement run or workflow.

Each run commits independently. The command continues after an individual
blocked, changed or failed outcome and emits a human or `--json` report with:

- `resumed`: the API accepted the continuation with the next resume count.
- `blocked`: diagnostics or the resume API rejected safety, quota, plan or deployment gates.
- `changed`: run status or count changed, or the server rejected a stale count.
- `failed`: a required read, scope check, or resume request could not be confirmed.

Exit status is 0 only when every selected run reports `resumed`; mixed outcomes
return 1 after writing the report. A scope/read failure during the initial full
selection check exits before any write. Reports include metadata and blocker
codes, not run inputs, outputs, error details or tenant identities. Status and
resume count are the last observations, rather than a final execution result.

A `resume_unconfirmed` or `resume_response_unconfirmed` result can mean the
server committed but its response was lost or unusable. Retry the **same run
IDs, original counts and batch key**. The CLI never refreshes an expected count
to make an interrupted request pass. If the previous request advanced the run,
the retry reports `changed` and does not issue another resume for it. A changed
run might have been resumed by another operator; inspect `gregale workflows
resumes RUN_ID` for the recorded actor and continuation history. To request a
later intentional continuation, inspect a fresh preview and select its new
count with a new batch key.

This action does not clear manual/failure pauses, replay events, change schedule
cursors or replay missed schedule fires. Existing run continuations use their
own diagnostics and server admission checks independently of automatic starts.

The API provides `GET` and `PUT /v1/apps/{slug}/workflows/stored/{name}/failure-policy`
and `POST /v1/apps/{slug}/workflows/stored/{name}/failure-policy/resume`. Reads require
app read scope; configuration and resume require deploy write scope. Apply
migration `20261010071515271_automation_failure_pauses.sql` and update all
scheduler, admission and API replicas before enabling policies. Rollback
requires explicitly resuming every failure pause and removes policies, guard
state and guard history. Committed webhook events remain deliverable.

For terminal failures, enable the opt-in `stored_workflow_failures` preset for an
app on Hobby or higher. It sends a signed webhook when at least one run has
failed or died in the last five minutes, with a 30-minute default cooldown:

```sh
printf '%s\n' "$ALERT_SECRET" | gregale alert preset enable stored_workflow_failures \
  --app billing --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

This policy covers all workflows in the app. Cancellations, successful runs,
and intermediate retry attempts do not count. Evaluation occurs on the next
alert evaluator tick; repeated ticks during cooldown do not create duplicate
deliveries. The same failure leaves the observation window after five minutes.
A later failure can notify again once cooldown has elapsed. Clearing a breach
returns the rule to `ok` without a separate recovery webhook. Customize the
threshold, observation window and cooldown using the existing alert rule API or
`gregale alert update`. This is an aggregate notification policy rather than
one notification for every failed run.

App-scoped workflow notifications include `app_id`, `workflow_runs_path`, and
`stored_workflows_path`. Failure notifications also include
`workflow_dead_runs_path`; the two run links separately select `failed` and
`dead` runs. These are authenticated API paths relative to your Gregale endpoint,
not public links. They show current run lists, not a snapshot restricted to the
alert window: a long-running workflow can finish inside the window despite
being created earlier. Retrieve a run's diagnostics using
`GET /v1/workflows/runs/{id}/diagnostics`. Notifications do not copy run inputs,
outputs, error messages, or tenant identities. Account-wide rules omit app links;
app lookup failures omit links without suppressing the notification.

Apply migration `20261010071621984_automation_failure_alert.sql` to expose the
failure preset. Updating the alert evaluator adds the investigation paths to
all five workflow metrics. Existing rules and notification delivery history
remain intact; no new alert is enabled automatically. Disabling the rule stops
future evaluation. Rolling back removes the preset from the catalog while rules
already created from it continue to use the existing `workflow_failures` signal.

For sustained backlog, enable the opt-in `stored_workflow_backlog` preset for an
app on Hobby or higher. It sends a signed webhook when a stored workflow has been
due for at least five minutes, with a 30-minute default cooldown:

```sh
printf '%s\n' "$ALERT_SECRET" | gregale alert preset enable stored_workflow_backlog \
  --app billing --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

The preset covers all stored workflows in the selected app. Its age signal matches
queue health, including work blocked by concurrency limits, overdue timers and
event/callback timeouts, and expired worker leases. Intentional future waits and
retry backoff do not count. `window_spec` does not average or restrict this
current-state age. The next evaluator tick observes a threshold breach; a
sustained breach can notify again after cooldown. A cleared backlog returns
the rule to `ok` without a separate recovery webhook.

To choose a different threshold, create a custom alert using
`workflow_due_age_seconds`, comparison `gte`, and a threshold in seconds.
After a notification, inspect `gregale workflow stored health --app billing --name
STORED WORKFLOW` for waiting reasons and `gregale workflows list --app billing` for
the runs. Notifications contain aggregate values rather than run payloads or
tenant identities. Apply the backlog-alert migration and update all evaluators
before enabling this preset. Remove its rules before downgrading.

App-handler steps retry HTTP 408, 425, and 429 using the configured attempt
budget. A downstream `Retry-After` (seconds or HTTP date) extends ordinary
backoff, capped at one hour, and survives scheduler restarts. Ordinary 4xx
validation/authorization errors remain terminal. All attempts retain the
same step idempotency key; app handlers must deduplicate their side effects.
Unsafe outbound actions keep their existing no-retry policy.

Workflow dispatch uses up to 32 execution slots per scheduler. Every tick
offers four, and one more while every slot is busy, so capacity grows with
load; each slot drains at most eight runs. A slot waiting on a handler does not
hold up other slots' bookkeeping. Eligible applications
and tenant scopes within an application take turns according to their persisted
last claim; older due work wins within a scope. A large backlog cannot keep an
eligible scope behind all its older runs.

At most two unexpired running stored workflow claims belong to one app, and at most
one belongs to one tenant within that app, across upgraded scheduler workers.
An app may queue runs while dispatch slots are idle, because these caps reserve
capacity for other apps.
Timers, callbacks, condition waits and pending retries release dispatch capacity
while parked. These dispatch caps are separate from the plan's active-run quota,
`max_concurrent_runs`, handler invocation limits and foreach parallelism. A wait
may still consume the workflow definition's concurrency budget.

Fair selection survives worker restarts and history pruning. Executing handlers
finish or time out before their slots become available; dispatch does not
preempt them or guarantee latency when all slots are busy. Apply the database
migration before upgrading workers; all workers must be upgraded for the new
dispatch caps and fairness to apply consistently.

## Recovery plans and policies

### Saved run recovery plans

Save an explicit reviewed selection without resuming any runs:

```sh
gregale workflow stored recover --app billing --name paid-invoice \
  --run RUN_UUID:2 --idempotency-key outage-batch-1 --plan-out recovery.json
gregale workflow stored recovery run --plan recovery.json --journal attempt-1.jsonl
```

The versioned JSON plan pins the app identity, stored workflow, original resume counts,
run IDs and stable batch key. Creation checks current scope and run state. Review
this file before execution; it contains no run inputs, outputs or error details.
Execution checks every run's scope before any resume, then rechecks eligibility
and counts immediately before each independently committed request.

Plan and journal paths must be new files; both use owner-only permissions. The
journal syncs a start record, an attempted record before each run, its result,
and a completion record. Failure to persist stops further attempts. An attempt
without a result may have committed; a completion record means all selections
were processed, not that all resumed or that their workflows completed.

Retry an interrupted batch with the same plan and a **new** journal path. Original
counts and keys remain fixed, including after a lost response. Advanced runs are
reported as changed and never automatically resumed at a newer count. A changed
result does not establish who resumed the run; inspect its resume history.
Plans are limited to 25 runs and 64 KiB. Each execution has a two-minute timeout.
The journal is local recovery evidence, not a server-side transaction or audit log.

### Reviewed recovery of stored workflow event routing

Save selected retained events whose captured stored workflow recipient currently has
one supported routing replay action:

```sh
gregale workflow stored event-recovery-plan --app billing --name paid-invoice \
  --source billing --event invoice-42 --event invoice-43 \
  --idempotency-key incident-1 --out event-recovery.json
gregale workflow stored event recover --plan event-recovery.json --journal events-attempt-1.jsonl
```

Export is read-only. The plan pins the app identity, published stored workflow version,
source/event IDs, captured subscription, routing generation/replay count, exact
routing replay action and stable batch key. It excludes event payloads and error
text. Creation fails for ambiguous recipients or unavailable actions. Execution
rechecks each event immediately before requesting replay and journals attempted
and outcome records with disk sync before continuing. Files must be new and use
owner-only permissions. Both commands have a two-minute timeout; selections are
limited to 25 events, plans to 64 KiB, and each receipt scan to 25 pages.

Outcomes distinguish queued, changed, blocked and failed. Expired retained events
are blocked without an age override. Missing retained evidence is unavailable;
it cannot establish whether an event expired or never existed. Changed revisions,
routing generations or actions require a new review. Already admitted workflows
use run recovery rather than routing replay. Admission pauses and other server
rules remain authoritative when the routing queue processes the replay.

Retry with the **original plan** and a new journal path. Per-event idempotency keys
remain stable and advanced routing generations are never adopted. A lost reply
is unconfirmed; consult the journal and receipt. The journal completion record
means all selected events were processed, not that all were queued or completed.
Saved plans send `recovery_expectation` with the published stored workflow version,
routing replay count and generation (when present). The server compares these
values while holding the publication lock and routing receipt locks, then queues
the replay in the same transaction. Changes return HTTP 409
`event_recovery_changed` without mutating the receipt. Existing replay requests
that omit expectations retain their behavior. Guarded replay supports acceptance
recipients; historical backfill recovery uses its separate job controls.
Admission rules still apply when the queued recipient is processed.

### Durable recovery of reviewed stored workflow events

Submit a saved event plan as a server-managed job:

```sh
gregale workflow stored event-recovery start --plan event-recovery.json --rate 5 --reason "invoice routing incident"
gregale event recovery get JOB_ID
gregale event recovery items JOB_ID
gregale event recovery pause JOB_ID --yes
gregale event recovery resume JOB_ID --yes
gregale event recovery update JOB_ID --rate 2 --yes
gregale event recovery cancel JOB_ID --yes
```

The job continues after the CLI disconnects. It protects pending retained receipts
within the existing job lifetime and shares existing account job quotas and rate
limits. Pause and cancel stop future routing admissions; already queued workflows
continue. Each item uses the saved published version, replay count and routing
generation, checked atomically before admission. Changed items are skipped rather
than following newer failures. Delivery deadlines still apply; historical
backfills use their separate job controls.

Retry an uncertain submission with the original plan and rate. Its stable plan
key identifies the retained job, including after completion; changing the plan or
initial rate with the same key conflicts. Use the rate control to adjust an
existing job. Idempotency lasts for job retention. Preview only confirms retained
selection and preflight reports workflow fence eligibility as unknown; the worker
performs the authoritative checks. `queued` records routing admission, not
workflow success. Saved **run** recovery plans can also be submitted with `recovery-start`.

### Server-managed run recovery

Submit a reviewed saved run plan to continue recovery after the CLI disconnects:

```sh
gregale workflow stored recovery create --plan recovery.json --rate 2 --reason "incident recovery"
gregale event recovery get JOB_ID
gregale event recovery items JOB_ID
gregale event recovery pause JOB_ID --yes
gregale event recovery resume JOB_ID --yes
gregale event recovery update JOB_ID --rate 1 --yes
gregale event recovery cancel JOB_ID --yes
```

The plan's original run IDs, workflow name and resume counts are frozen. The server checks each count and safe continuation atomically with resume admission. Changed or unsafe runs are skipped; concurrency pressure leaves the item pending for a bounded retry. Cancelling stops pending admissions and preserves admitted resumes. Items expose `workflow_run_id` and `expected_resume_count`; `queued` means the resume was admitted, so inspect the workflow run for its execution outcome.

Retry submission with the original plan and original rate after a lost reply. The retained job is returned even after completion; changing the selection or initial rate with the same plan key conflicts. Change the running job's rate through `recovery-rate`. The shared recovery quota applies across modes. Jobs expire after 24 hours and remain retained for 30 days; pausing does not extend expiry. Preflight is advisory and reports native resume eligibility as unknown. The local `recovery-plan --journal` command remains available.

### Observe resumed workflow execution

`events recovery-status JOB_ID` includes an execution summary for native run recovery jobs. `events recovery-items JOB_ID` includes each admitted run's execution observation, with `source: workflow_run`. The job and item admission counters keep their existing meaning: completing admission does not mean resumed execution succeeded.

Observations track the original reviewed resume count plus one, supported by the retained resume record. Pending runs report queued, running or awaiting-event runs report running, succeeded runs report succeeded, and failed/dead runs report failed. Operator-cancelled terminal runs report cancelled. Without an earlier saved terminal result, a later resume reports superseded and never contributes its completion time or success to this recovery job. Missing or pruned run/resume history reports unknown.

Native observations are read-only. The first confirmed terminal outcome is saved with its recovery item and survives workflow-history pruning for the recovery job retention period. Saved observations use `source: recovery_result`, `evidence_source: workflow_run`, and a `recorded_at` timestamp. `saved_results` counts these outcomes; invocation dispatch `attempts` stays zero. Once saved, an earlier failure remains authoritative if the run is resumed again. A later resume is superseded only when no earlier terminal outcome was saved. Use job status for execution summaries; recovery discovery lists remain admission metadata only.

### Native recovery execution-finished notifications

Subscribe an application webhook to `event_recovery.execution_finished`. After admission completes, is cancelled, or expires, the scheduler captures one notification when all admitted resumes have saved terminal results. `all_succeeded` means every admitted resume succeeded; otherwise the outcome is `finished_with_non_success`. Admission counters remain separate. Unknown or active outcomes block capture; jobs admitting no resumes produce no execution notification.

The payload includes outcome counts, `execution_finished_at`, and `items_url`, an authenticated relative API link to paginated recovery items. Items retain run IDs even when workflow history is gone; retained runs can be inspected through `/v1/workflows/runs/{id}`. Inspect `events recovery-notifications JOB_ID` for recipient and delivery evidence. Existing outbox retries apply; receivers should deduplicate by `event_id`.

### Retry saved native workflow recovery failures

Preview a terminal native recovery job's saved outcomes and current resume eligibility:

```sh
gregale workflow stored recovery-retry-plan --app worker --job JOB_ID
```

Review the displayed positions, then save only the eligible failures you intend to retry:

```sh
gregale workflow stored recovery-retry-plan --app worker --job JOB_ID \
  --position 2 --position 4 --key incident-retry-1 --plan-out retry.json
gregale workflow stored recovery create --plan retry.json --rate 2
```

Preview and plan creation use read-only calls. Saving requires explicit positions, a key, and a new file. A target retains the resume count of the saved failed outcome; it never follows a newer resume. Succeeded, cancelled, unsaved, pruned, changed, or unsafe runs cannot be selected. The server verifies the parent result when creating the child and rechecks count and safety at admission. Changed or unsafe targets are skipped. Use a new key for a different reviewed selection; repeating the same retained request returns the existing job.

Retry plans require durable `recovery-start` submission; `recovery-plan` cannot execute them locally. An admitted child retains parent job/item lineage and can continue after its parent is pruned. Creating a new child requires retained parent evidence. Retries remain explicit operator actions.

The retry preview now uses server-side preflight. It reports `eligible`, temporary `waiting` capacity pressure, and permanent blockers separately. A capacity-only wait remains selectable: the durable worker will wait for admission capacity. Safety blockers, missing saved outcomes, changed generations, missing runs, and exhausted resume limits cannot be selected. Permanent blockers take priority when capacity is also full.

API clients can use `GET /v1/event-recoveries/JOB_ID/preflight?retry=true` to review a terminal native parent's saved outcomes in one consistent read-only snapshot. `reviewed_count` counts parent items and `sample` contains all of them (at most 25); `pending_count` is zero in this view. Timing fields describe the parent job. Ordinary preflight also reports pending native workflow eligibility. Neither view reserves capacity or replaces the checks at durable creation and admission.

Recovery execution deadlines and stalls are advisory. When submitting a reviewed native workflow recovery plan, use `--execution-deadline 1h --execution-stall 15m` on `gregale workflow stored recovery create` to select thresholds. The deadline includes scheduled and capacity waits; the stall threshold excludes those waits. Without flags, deadlines are disabled and stalls use 15 minutes. Health observes only the admitted generation, and `event_recovery.execution_attention` captures the first attention signal per job through the normal durable notification pipeline. Inspect the linked run diagnostics before explicitly cancelling or preparing another retry.

To resolve an execution attention finding, inspect `gregale workflow stored recovery health --app billing`. Its run and diagnostics links identify the admitted generation. Preview one cancellation with `gregale workflow stored recovery-cancel-preview --app billing --job JOB --position 1 --resume-count 1 --reason 'provider incident'`. To apply that reviewed action, use `recovery-cancel` with the same arguments and `--yes`. The server rechecks the frozen generation at commit time; a newer resume is refused. Cancellation is cooperative and does not undo external effects. Successful actions appear in `gregale event recovery history JOB` with the actor, reason and target generation. Use `recovery-retry-plan --app billing --job JOB` to review confirmed failed results after admission ends.

### Saved recovery response plans

Save a reviewed mixture of cancellations and retries from one native recovery job:

```sh
gregale workflow stored recovery-response-plan --app consumer --job JOB_UUID \
  --key incident-20261011 --reason 'provider incident' \
  --cancel-position 1 --retry-position 2 --plan-out response.json
gregale workflow stored recovery-response check --plan response.json
gregale workflow stored recovery-response apply --plan response.json --yes
gregale workflow stored recovery-response status --plan response.json
```

Positions are explicit and repeatable; a target can appear only once. Cancellation requires an admitted active generation. Retry requires a retained failed execution and unchanged eligible generation. Plan creation and preview do not apply actions. The saved file pins the app, parent job, run generations, reasons and action IDs; protect and review it before applying.

Reapply the same saved plan after interruption. Cancellation receipts retain the original decision, including refusals, even after the run advances. Retry actions reuse the original child job. Status only reads retained receipts and outcomes. Retry admission does not imply completed execution. Unresolved, refused, pending and unknown outcomes return exit code 5; terminal retry execution outcomes are explicitly reported, including failures. Missing or expired evidence remains unresolved. A newly reviewed action needs a new plan key; editing an existing action's payload under its identifier conflicts with its retained request. Actions commit individually.

### Preview recovery policies

An app can opt into scheduled preparation of response plans from retained native recovery jobs. Policies match explicit workflow names and saved failures, stalls or deadline overruns. The initial mode is `preview`; operators still preview and apply exported plans explicitly.

Inspect the current version with `gregale workflow stored recovery-policy --app consumer`. Save this configuration as `policy.json`, using the version you observed (`0` for a new policy):

```json
{
  "expected_version": 0,
  "policy": {
    "enabled": true,
    "mode": "preview",
    "workflow_names": ["invoice-notification"],
    "triggers": ["saved_failure", "deadline_exceeded"],
    "failure_statuses": ["failed", "dead"],
    "max_resume_count": 3,
    "cooldown_seconds": 300,
    "hourly_action_budget": 10,
    "rate_per_second": 1,
    "reason": "Review invoice delivery recovery"
  }
}
```

```sh
gregale workflow stored recovery-policy --app consumer --file policy.json
gregale workflow stored recovery-policy history --app consumer
gregale workflow stored recovery-policy history --app consumer \
  --review REVIEW_UUID --plan-out prepared.json
gregale workflow stored recovery-response check --plan prepared.json
gregale workflow stored recovery-response apply --plan prepared.json --yes
gregale workflow stored recovery-response status --plan prepared.json
```

Policies are disabled by default and only `preview` mode is supported. A stale `expected_version` conflicts; read the policy before changing it. Set `enabled` to false to stop future preparation. Existing plans remain available for explicit application.

`max_resume_count` bounds total resumes of a run, including manual resumes. The hourly budget counts prepared actions, and the cooldown applies across the app's selected workflows. Preparing a plan consumes budget even if it is never applied. These policy controls do not limit separate manually submitted recovery work. Repeated matches for the same run, generation and action reuse preparation evidence across policy versions. Matching failures use retained outcomes and existing retry eligibility; attention cancellation uses the original admitted generation.

Reviews freeze settings and explain each matched target's prepared or suppressed outcome. Evidence follows recovery-job retention. At most 1000 reviews are retained per app; `review_limit` stops new preparation until retention frees capacity. Evaluation scans five jobs per policy per minute in a cycling identifier order, so large backlogs can delay review. Unknown or changed generations are never substituted with newer targets. Native recovery monitoring is the source of attention thresholds; a policy does not introduce its own deadline or stall timer.

### Review policy-generated recovery plans

Record an operator decision on a retained prepared plan:

```sh
gregale workflow stored recovery-policy history --app consumer --review REVIEW_ID --decision approved --reason "Provider recovered; retry reviewed"
gregale workflow stored recovery-policy history --app consumer --review REVIEW_ID --decision dismissed --reason "Incident resolved externally"
gregale workflow stored recovery-policy history --app consumer --review REVIEW_ID --status
```

Use one operation per command. Decisions require a prepared plan and a reason.
The first decision is immutable; repeating the same decision and reason returns
its original actor and timestamp. A different decision or reason conflicts.
Approval records review only. Preview and explicitly apply the exported plan
using the existing recovery-response commands. Dismissal retains the original
plan, target deduplication and preparation budget evidence; manual apply remains
possible. Decisions share the parent recovery job's retention.

`--status` checks live generations and terminal targets without changing the plan.
It reports ready, stale, unknown, partially_applied, applied or no_plan, with
per-action reasons. Durable action receipts take precedence over live changes.
Applied means cancellation succeeded or a retry child was created; inspect retry
execution with `recovery-response-status`. Capacity waits remain eligible retries.
Review disposition is separate from freshness: approved plans can become stale.

### Recovery review-ready notifications

Subscribe an enabled app webhook to `stored_workflow.recovery_review_ready` (an empty
filter also receives it). Schedd captures one logical event per prepared plan,
including existing retained plans that still need review. The payload includes the
app, recovery job, frozen run IDs and generations, trigger, action, review/status
links, and CLI inspection/export commands. Review inspection shows capture status,
reason, receiver count and event ID; use webhook deliveries for HTTP outcomes.

Capture freezes the recipient list. No matching receivers produces `no_receivers`;
adding a webhook later does not replay old reviews. Capture is durable across
restarts, and failed HTTP deliveries reuse the existing retry/backoff machinery.
Receivers should deduplicate by payload `event_id`, since a lost acknowledgement
can result in another HTTP request for the same event.

Approval or dismissal, application, stale targets, and pruned reviews suppress
pending transmissions. Unknown freshness or temporary check errors retain work for
retry. Suppressed deliveries appear as dead with a suppression reason; retrying
manually still checks the review. An already started request cannot be recalled.
Approval and applying actions remain separate explicit operations.

Recovery review status also reports `outcome` and `complete` separately from
application `status`. An applied retry can still be pending or running. Each
action includes its workflow run, exact execution generation, retry child and
items link, and proof source and timestamp when final. Saved terminal retry
results prove success, failure or cancellation. Terminal skipped admission is a
failed action; cancelled admission is cancelled. Missing or superseded execution
evidence stays unknown. All actions must be final before the plan is complete;
mixed success and cancellation reports `completed`, and any failure reports
`failed`.

Subscribe an enabled app webhook to `stored_workflow.recovery_review_finished` to
receive one durable completion event per prepared plan. It carries the frozen
final status, proof for every action, app and parent job identifiers, review and
status links, and a CLI status command. Inspect `completion_notification` on the
review for the captured result, event ID and receiver list. A capture without
receivers is retained and does not replay to later subscriptions. Later review
decisions or workflow retries cannot change the captured result. Retention follows
the parent recovery job. Check the webhook delivery ledger for HTTP attempts and
acknowledgement; deduplicate deliveries by `event_id`.

### Bounded automatic recovery retries

Opt in with `mode: "automatic_retry"` using the existing versioned recovery-policy
configuration command. This mode requires `saved_failure` as its only trigger,
explicit workflow names and failure statuses, `hourly_execution_budget` (1..100),
and `pause_after_failures` (1..10). Preview remains the default and cancellation
remains manual.

```json
{
  "expected_version": 0,
  "policy": {
    "enabled": true,
    "mode": "automatic_retry",
    "workflow_names": ["paid"],
    "triggers": ["saved_failure"],
    "failure_statuses": ["failed", "dead"],
    "max_resume_count": 3,
    "cooldown_seconds": 300,
    "hourly_action_budget": 10,
    "hourly_execution_budget": 3,
    "pause_after_failures": 2,
    "rate_per_second": 1,
    "reason": "bounded incident recovery"
  }
}
```

```bash
gregale workflow stored recovery-policy --app consumer --file automatic-policy.json
gregale workflow stored recovery-policy --app consumer
gregale workflow stored recovery-policy history --app consumer
```

Automatic retries use the same exact-target, keyed child recovery path as manual
application. One automatic retry per app must reach a proven final result before
another starts. Unknown evidence defers progress. The hourly execution budget
counts child creation separately from the preparation budget, and spent execution
budget and cooldown survive policy updates. Inspect `automatic_execution` on each
review for its pending, applied or suppressed actions, child IDs and saved results.
Manual application is recognized and does not consume the automatic budget.

After the configured consecutive proven failure threshold, the policy reports
`status: "paused"` and `automatic_execution` contains the failure count, pause
time and reason. Both preparation and automatic execution stop. Subscribe an app
webhook to `stored_workflow.recovery_policy_paused` for a durable notification carrying
the policy version and inspection link. Receivers deduplicate by `event_id`; the
webhook ledger records delivery attempts. Existing `stored_workflow.recovery_review_finished`
notifications report completed plan results. Automatic plans do not send a
review-ready notification.

To resume while retaining queued retries, use `recovery-policy --control resume`
with an operator reason, as described below. A full policy replacement with the
observed `expected_version` also clears the pause and failure streak, but
suppresses older pending automatic intents.
Disabling or replacing the policy suppresses older pending automatic intents;
already created child jobs use the existing recovery controls. Only newly prepared
plans under the current enabled version execute automatically. Older preview or
superseded plans remain available for manual review and application. Cancellation
is never automatic.

### Inspecting and controlling the automatic retry queue

```sh
gregale workflow stored recovery-policy --app consumer --queue
gregale workflow stored recovery-policy --app consumer --control pause --reason "Investigating provider errors"
gregale workflow stored recovery-policy --app consumer --control resume --reason "Provider recovered"
```

The queue shows total pending retries, unresolved applied children, remaining hourly execution budget, cooldown expiry, and each returned action's last waiting reason. `outstanding` means final proof has not been recorded; it does not assert the child is running. Use `recovery-policy-reviews --app consumer --review REVIEW_ID --status` for live execution evidence. JSON output includes the observed policy, review IDs and policy versions. The first 100 entries are returned in review order; `truncated` indicates more retained entries. Charges survive policy changes and review pruning.

Pause stops preparation and automatic application while retaining the current policy version and queued retries. Already committed children continue. Resume clears the pause and consecutive failure streak, then rechecks each frozen target; spent budgets and cooldowns remain in effect. Controls require an enabled `automatic_retry` policy, a reason, deploy write access and MFA. The CLI reads the current policy and sends the observed version, update timestamp and pause timestamp to `POST /v1/apps/{slug}/recovery-policy/control`. Concurrent changes produce a conflict. Each accepted operation retains its trusted actor and reason in durable control audit storage.

`GET /v1/apps/{slug}/recovery-policy/queue` requires read access and MFA. Queue reasons are observations from the last worker evaluation and do not authorize a retry. Operator pauses do not emit the automatic repeated-failure webhook; that event remains a historical failure fact once per configuration version.

### Suppressing a queued retry and receiving blocked-queue notifications

Inspect `gregale workflow stored recovery-policy --app consumer --queue`, then suppress one pending intent using the returned review, action and current policy version:

```sh
gregale workflow stored recovery suppress --app consumer --review REVIEW_ID --action ACTION_ID --policy-version 4 --reason "Recovery handled externally"
```

Suppression requires deploy write and MFA. It records the operator reason, trusted identity and timestamp on the retained action, and removes that intent from automatic execution. Repeating the same reason returns the original receipt while the policy version remains current. Changed reasons, stale versions and applied intents conflict. Manual recovery remains available. No child is cancelled, no execution slot is released for an unresolved applied child, and no spent budget is refunded. Inspect the retained review to read the suppression receipt; suppressed intents no longer appear in the pending queue.

Subscribe an app webhook to `stored_workflow.recovery_queue_blocked` to receive a historical notification when fresh inspection finds missing or superseded evidence blocking a pending or applied automatic retry. Capture occurs once per action, with a stable `event_id`, frozen recipients and queue/review status links. Normal admission waits, running retries, budget or quota waits, inspection errors, dismissed pending reviews, and paused or disabled policies stay quiet. If no receiver exists at capture, `blocked_notification.status` is `no_receivers`, with no replay to later subscribers. `queued` means admitted to the webhook ledger; use the ledger for delivery results. The event may arrive after the block resolves or the intent is suppressed. Its receipt remains visible on the retained review and, for still-pending or unresolved actions, the queue.

### Queue age and last worker evaluation

Use `gregale workflow stored recovery-policy --app consumer --queue` to see when each retained retry was prepared, its age, the last execution-worker check, and how long the current observed wait has persisted. JSON queue entries expose `queued_at`, `queued_age_seconds`, optional `waiting_age_seconds`, and `action.last_evaluation` (`evaluated_at`, `reason`, optional `waiting_since`). Ages are whole nonnegative seconds relative to the response's `observed_at`; future timestamps from clock skew produce zero age.

Queue age starts at frozen review preparation and includes time after child creation. Waiting age starts with the worker's first observation of the current consecutive reason, survives repeated checks and restarts, and resets when the reason changes. Reasons distinguish `hourly_budget`, `cooldown`, `execution_quota`, `evidence_unknown`, `outstanding_retry`, `admission_wait`, `execution_wait`, `retry_running` and `inspection_error`. Child creation, saved final proof and selected-intent worker suppression record `child_created`, `result_recorded` and `suppressed` without a waiting start. `retry_running` describes the last observed execution outcome; its wait measures time awaiting final proof.

These are historical observations. Reads, blocked-notification capture, policy replacement and operational pause/resume do not refresh them. A paused or disabled policy can retain an old reason even when circumstances have changed; compare its evaluation time and current policy state. Legacy intents and pending retries not yet selected have no evaluation or waiting age, displayed as “not recorded.” The worker evaluates the next pending intent when a committed child holds the slot; later pending entries may remain uninspected. Missing evidence never completes a child, releases its slot or refunds spent budget.

### Previewing a policy replacement

Save the proposed policy and the currently observed `expected_version` in the existing policy update JSON format, then inspect its impact:

```sh
gregale workflow stored recovery-policy --app consumer --file policy-update.json --preview
```

The CLI sends only `POST /v1/apps/{slug}/recovery-policy/change-preview`. This endpoint requires read access and MFA, and returns conflict if the configuration version changed. Version zero previews initial configuration without creating a policy. Preview never rewrites your file or advances the policy version, worker evaluations, scheduling cursors, budgets, retries or notifications. Apply the reviewed file separately with the same command without `--preview`; deploy write access, MFA and the existing version check still apply.

Every full replacement suppresses old pending automatic retries, even with unchanged filters. The preview identifies them as `would_suppress`. Unresolved applied retries from any version remain `retain_slot`; changing filters, disabling stored workflow or lowering budgets neither cancels nor completes them. Spent execution charges remain counted against the proposed hourly execution budget and cooldown. Retained preparation targets and preparation history also remain in effect: suppressed generations can still be `already_prepared`, so replacement does not necessarily create another plan. Existing preview-mode plans are not converted to automatic retries. Replacement clears an automatic pause and failure streak; inspect `current.automatic_execution` before applying.

The snapshot reports current/proposed settings, pending/outstanding counts, up to 100 queue effects, proposed preparation/execution headroom and cooldowns. It inspects up to five newest retained recovery jobs matching proposed workflow names, and returns up to 25 matching candidate decisions. `entries_truncated`, `jobs_truncated` and `candidates_truncated` explicitly indicate omitted results. `would_prepare` simulates preparation; it does not create an executable plan or guarantee quota, admission or immediate execution. Eligible disabled-policy targets report `policy_disabled`.

This sample describes retained recent evidence rather than all historical failures or the scheduler's next UUID-ordered poll. Queue progress, operational controls and failure pauses can change while the configuration version stays the same. Use the observation timestamp, review current queue state before applying, and rely on execution-time evidence and quota checks. Preview grants no additional write or retry authority.

### App recovery automation health

Use `gregale workflow stored health --app APP` to inspect recovery policy health, or
add `--name NAME` for the existing named stored workflow run statistics. App-wide
health accepts `--stall-after-seconds SECONDS` (default 300; range 1–86400) and
`--json`. This request-local threshold is not a saved notification setting.

The read-only `/v1/apps/{slug}/recovery-policy/health` endpoint requires app read
access and MFA. It returns `healthy`, `inactive`, `paused`, `blocked`, `stalled`
or `unknown`, a reason, and the consistent bounded recovery queue snapshot.
Paused takes precedence. Stalled means a child evaluation is overdue, or
pending work lacks recent worker evaluation while execution is permitted. A
fresh pending evaluation demonstrates activity across the pending queue; an
untouched backlog entry alone does not imply a stall. Pending work behind an
unresolved child, active cooldown and exhausted execution budget are expected
blocks. Fresh evidence
that a child is running does not imply a stall based on its execution age.
Disabled policies still expose unresolved applied children.

Queue inspection is bounded. An incomplete sample without a known blocker
reports unknown. `sample_last_progress_at` describes progress in retained queue
entries only; it can disappear after completion and is not an app-wide last
success. Healthy means no blocker observed, not proof of scheduler liveness.
Use `gregale workflow stored recovery-policy --app APP --queue` for individual
worker observations. No health read sends alerts, changes policy or releases
an execution slot.

### Health alert and recovery webhooks

Opt in by adding `health_alerts` to your recovery policy JSON, then use the
existing preview and version-checked full replacement commands:

```json
"health_alerts": {
  "enabled": true,
  "stall_after_seconds": 300,
  "alert_after_seconds": 300
}
```

Both durations must be explicit integers from 1 to 86400 seconds. These saved
monitor settings are independent of `workflow stored health --stall-after-seconds`.
Changing them uses a full policy replacement, which also suppresses old pending
retries; preview the file first.

Subscribe an app-scoped webhook to `stored_workflow.recovery_health_alert` and
`stored_workflow.recovery_health_recovered` (or use an empty filter). The scheduler
observes recovery health once per minute in bounded batches. A sustained
paused, blocked or stalled condition opens one incident and emits one alert.
Reason changes and repeated polls do not send more alerts for that incident.
Healthy or inactive health closes it and emits recovery after alert capture.
An inactive recovery reports disabled automatic execution, not child success.
Incomplete/unknown snapshots cannot declare recovery and defer an uncaptured
alert. Disabling health alerts closes an incident silently.

Alert receivers are frozen at capture. Recovery goes only to original alert
receivers still enabled and subscribed to recovery. No-receiver captures are
terminal and stay quiet when a webhook is added later. The signed delivery
payload has stable `event_id` and `episode_id`, app and policy identity, health
reason, observation times, queue counts and a health URL. Deduplicate by event
ID and correlate by episode ID; historical deliveries can arrive after health
changes or out of order.

`gregale workflow stored health --app APP` displays the saved monitor settings and
latest incident receipts. Its JSON response includes `queue.health_incident`.
These reads do not advance monitoring. The monitor does not retry or cancel
workflows, refund charges or release a child's slot. Because it runs in the
scheduler, complete scheduler outage requires a separate external heartbeat.

### Recovery history

Use `gregale workflow stored history --app APP` to connect retained review outcomes, operator controls, retry children, worker observations and health notification captures. Filter with `--workflow NAME`, `--run UUID`, or `--policy-version N`. `--created-after` and `--created-before` accept RFC3339 timestamps; the default window is seven days and the maximum is 30 days. Pages default to 25 entries and cap at 100. Pass `--after` the returned cursor with the same filters to continue; `--json` exports one page.

The timeline combines durable events (`durable: true` in JSON) and retained observations. New policy replacements preserve the accepted configuration, actor and reason. Automatic failure pauses, worker reason changes, incident openings/changes/closings and health notification captures preserve their original associations. `policy` contains the frozen configuration on `policy_changed` events; `episode_id` correlates incident events. `evaluation_reason` records the worker classification, while `reason` preserves the actual suppression cause. Result-recorded transitions retain the child outcome and its outcome reason. Repeated unchanged polls do not append events. Events survive removal of their source review/job and later health episodes.

Durable recording starts with installation of this feature; earlier transitions are not backfilled. Durable events retain 30 days from recording and cap at 10,000 per app. Writes enforce those bounds, reads omit expired events, and the scheduler removes expired rows in batches of 1,000 each minute. Scheduler outage or backlog can delay physical deletion. The cursor fixes the original time window and durable sequence ceiling, excluding later appends. Retention can still remove events during pagination.

Legacy policy, worker and health observation entries remain snapshots of the latest state. Legacy health receipts do not infer historical policy versions or reasons. Target-specific filters omit records lacking that association. Latest observations can move or disappear across pages. Reads never execute retries or refresh worker evaluations.

### Recovery effectiveness report

Run `gregale workflow stored report --app APP --since 7d` to inspect observed retry
applications, successes, failures, cancellations, missing results and pauses.
Use `--workflow NAME`, `--policy-version VERSION`, or RFC3339 `--created-after`
and `--created-before` boundaries (maximum 30 days). `--since` accepts whole days
such as `7d`, or Go durations such as `24h`; it cannot accompany `--created-after`.
Global `--json` exports the full report, including evidence and coverage notes.

The report groups by workflow and policy version and lists distinct actions per
observed blocker, with links into history. Timing is application-to-recorded-result
for matched child jobs, including polling delay. Missing results mean missing
proof in the chosen window, not proof of a running retry. Results can come from
attempts preceding the window. Counts summarize at most 10000 newest entries in
one consistent snapshot and explicitly flag truncation. Durable capture is
prospective, without backfill, and retains 30 days / 10000 events per app. Small
samples and retention gaps limit comparisons; these counts do not prove a policy
caused a recovery. Reading the report does not change stored workflow state.

### Historical recovery policy evaluation

Use `gregale workflow stored recovery-policy evaluate --app APP --file POLICY.json
--since 7d` to compare current configuration settings with a proposed policy
against retained historical review decisions. The file uses the existing
`{"expected_version": VERSION, "policy": {...}}` update shape; evaluation rejects
stale versions and does not update the policy. Use `--workflow NAME`, RFC3339
`--created-after` / `--created-before`, or global `--json` for a structured export.
The default window is seven days and maximum is 30 days. `--since` cannot accompany
`--created-after`.

The output separates exclusions, unknown eligibility, conditional preparation
results and observed execution pressure. Historical reviews don't capture each
run's failure status. Their policy filters prove a possible status set; narrowing
an overlapping status filter is unknown. The evaluator never uses a run's current
status as past evidence. Captured saved-failure decisions can establish workflow,
trigger and generation constraints. Other historical triggers and unproven
preflight conditions remain unknown.

Preparation replay starts with empty budgets, cooldown and deduplication state at
the window start and leaves unknown candidates uncharged. It models conditional
preparations in chronological review/position order, including same-review batches,
rolling hourly budgets and generation deduplication. Operational policy pauses are
not replayed. Recorded app-wide retry applications can establish execution budget
or cooldown pressure; an absence of that proof remains `insufficient_history`.
No hypothetical execution, admission, retry success or failure pause is predicted.

Sampling is bounded to the oldest 100 matching reviews, 500 decisions and 10000
recorded applications, with separate truncation flags. Application warmup reaches
back the longer policy cooldown or one hour and is still subject to durable
retention. Only candidates reviewed under past settings and still retained are
available: unseen failures, pruned reviews/jobs and pre-recording events cannot be
reconstructed. Follow review links and application evidence identifiers to inspect
the basis for each decision. The read-only API is
`POST /v1/apps/{slug}/recovery-policy/evaluate`, requiring read access and MFA.

New policy reviews capture durable candidate evidence: exact observed run status,
preflight outcome, generation, trigger and policy version. Historical evaluation
uses this evidence even after source reviews/jobs are removed, within the shared
30-day/10,000-event history limits. Older reviews retain uncertain status-filter
inference. Capture is prospective and does not include execution inputs/outputs.

Terminal workflow failures are now captured independently of recovery-policy
filters, including while no policy exists or a policy is disabled. Historical
policy evaluation includes this inventory when you widen workflow/status filters.
The CLI reports capture start, sampled failures, review coverage, missing retry
checks and failure-sample truncation. Never-reviewed failures have unknown retry
eligibility: a saved failure alone does not prove a recovery parent or safe resume.
The inventory has a separate 30-day/10,000-entry per-app retention budget and
survives run pruning. Capture excludes cancellations, inputs, outputs and error
text, creates no reviews, and never executes recovery actions.

Recovery policy evaluation includes separately timed `later_eligibility` checks for unreviewed failures. A bounded background observer runs even without a recovery policy and checks retained recovery parents, generation fences and resume blockers. Checks refresh no sooner than five minutes and become stale after 15 minutes. These observations do not change historical preparation totals or authorize retries.

## Recover a captured initial workflow failure

List retained initial failures, then inspect one with live resume diagnostics:

```sh
gregale workflow stored failure list --app billing --name invoice-flow
gregale workflow stored failure-recovery-plan --app billing --failure FAILURE_UUID
```

The preview explains changed generations, missing runs, unsafe resumes, operation ownership and capacity blockers. The inbox is prospective and partial: evidence lasts 30 days and shares a 10,000-entry per-app limit across all captured failure generations. Use `--after` with the returned cursor to page through the inbox.

Save an eligible target and explicitly submit the reviewed plan:

```sh
gregale workflow stored failure-recovery-plan --app billing --failure FAILURE_UUID --key incident-42 --plan-out first-recovery.json
gregale workflow stored recovery create --plan first-recovery.json --reason "Reviewed incident 42"
```

Plan preparation does not create a job or resume the workflow. The plan retains the captured failure ID and expected generation zero. Creation and admission recheck retained evidence and generation; resume rechecks safety and capacity. A run that advances after review cannot be resumed by the stale plan. Keep the same plan/key when reconciling an uncertain submission. Inspect the returned native job and its execution results to distinguish admission from workflow completion. Submit these plans through `recovery-start`; the per-run journal executor cannot preserve their evidence checks.

The API exposes `GET /v1/apps/{slug}/workflow-failures` and `GET /v1/apps/{slug}/workflow-failures/{failureID}/recovery-preview`. First recovery remains an explicit operator action; automatic policies still retry saved failed recovery generations only.

### Dry-run initial-failure policy filters

Preview policies can opt in to `initial_failure`; this trigger requires explicit
`failure_statuses` (`failed`, `dead`, or both). Use a candidate policy file in the
same `{ "expected_version": 0, "policy": { ... } }` format as `recovery-policy`:

```sh
gregale workflow stored failure-recovery-plan --app orders --failure FAILURE_UUID --policy candidate.json
```

The command checks enabled state, trigger, workflow and failure-status filters
against a fresh recovery preview. Add `--key first-recovery --plan-out first.json`
to save a plan only when both the policy and live checks qualify. Submission
remains explicit with `recovery-start`. JSON reports `policy_reason` and
`execution_pressure: "not_evaluated"`; cooldown and budgets are not reserved.
Setting this trigger on a preview policy does not schedule initial-failure
reviews. `automatic_retry` continues to accept only `saved_failure`.

### Durable first-recovery reviews

Save a preview recovery policy that includes `initial_failure`, explicit workflow
names and failure statuses. Preparation uses the saved version, rather than a
candidate file:

```sh
gregale workflow stored failure-recovery review --app orders --failure FAILURE_UUID --expected-policy-version 3
gregale workflow stored failure-recovery list --app orders --limit 25
gregale workflow stored failure-recovery review --app orders --review REVIEW_UUID
gregale workflow stored failure-recovery review --app orders --review REVIEW_UUID --decision submit --reason 'Provider recovered; reviewed safe resume'
gregale workflow stored failure-recovery review --app orders --review REVIEW_UUID --decision cancel --reason 'Handled manually'
```

Submission and cancellation require a non-empty operator reason. A stale submission
prints the recorded review and exits unsuccessfully, including in JSON mode.

A review stores the failure evidence, generation-zero target, saved policy and
actor-attributed transition history. It expires after 24 hours and is retained
for 30 days, with at most 1000 retained initial reviews per app. Preparation and
submission repeat live safety checks. Changed policies or unavailable evidence
make a prepared review stale; expiry blocks submission. Cancellation applies to
prepared reviews. Once submitted, use native recovery-job cancellation controls
for the returned `job_id`.

Preparation shares the policy cooldown and hourly-action budget with parent-job
review preparation. Submission independently enforces the same cooldown and
hourly-action budget across first-recovery submissions, including earlier policy
versions. Charges are retained after cancellation. All generation-zero native
run-job submissions prevent another pending job for the same run, and native
resume fences direct manual races by generation. Reads do not reserve capacity.
Pending reviewed-job admission also checks review expiry and policy version;
already admitted runs continue through the native workflow lifecycle.

Repeated preparation returns the same review for a failure and policy version;
repeated submission returns the same retained job without extra charges or audit
entries. Stale and expired dispositions are inferred on read and recorded when a
submission is attempted. Terminal reviews cannot be renewed under the same saved
policy version. A changed version creates a new review subject to existing charges.

Ready webhooks use `stored_workflow.recovery_review_ready` with `initial_failure_id`
and no parent `job_id`; delivery suppresses stale, cancelled or expired reviews.
Submitted jobs use existing recovery completion and execution-result notifications.
Review transitions are in `events`; job history and receipts are on the native
recovery job. Preparation and submission are operator actions. Automatic first
recovery remains disabled.

### Worker preparation of first-recovery reviews

An enabled `preview` policy that includes `initial_failure` now lets the policy
worker prepare durable first-recovery reviews. Explicit workflow names and
failure statuses are required. The existing `stored_workflow.recovery_review_ready`
notification includes the captured failure ID and the command to inspect the
review. Operators still submit or cancel the review explicitly.

Each policy poll scans a bounded page of retained failures. A durable cursor wraps
through the inbox, revisiting temporary blockers without allowing an early
blocked page to permanently hide later failures. Cancelled, expired or submitted
reviews are not recreated under the same policy version. Policy updates reset the
scan; fresh checks, cooldowns and shared preparation budgets still apply. Worker
preparation records the system actor `initial_recovery_policy`.

This is a preparation dry run: it does not create recovery jobs or resume runs.
Blocked candidates have no review until they become eligible, and ordinary budget
or safety blockers do not consume preparation budget. Large inboxes can take
multiple polls to traverse. A crash after advancing the cursor is retried when
the scan wraps.

### Opt-in automatic first recovery

Use `automatic_initial` to let the worker submit qualifying generation-zero
failures after preparing their durable reviews. The workflow runtime must be
available when enabling the policy through HTTP. Start with `preview` to inspect
which failures qualify, then explicitly change the saved policy mode and limits:

```json
{
  "expected_version": 3,
  "policy": {
    "enabled": true,
    "mode": "automatic_initial",
    "workflow_names": ["recover"],
    "triggers": ["initial_failure"],
    "failure_statuses": ["dead"],
    "max_resume_count": 1,
    "cooldown_seconds": 300,
    "hourly_action_budget": 10,
    "hourly_execution_budget": 2,
    "pause_after_failures": 2,
    "rate_per_second": 1,
    "reason": "Bounded first recovery after provider failures"
  }
}
```

Only `initial_failure` is allowed in this mode. The automatic worker submits one
prepared review at a time using fresh policy, expiry, retained evidence,
safe-resume and native quota checks. Preparation charges the action budget;
automatic job creation charges the separate execution budget. Automatic initial
and saved-failure execution charges survive policy updates and mode switches.
Cancellation, a paused admission or another unsuccessful admission does not
refund a submitted job's execution charge.

One unresolved recovery blocks another automatic first submission for the app.
Pending native jobs, including paused jobs, and unresolved automatic or running
recoveries retain the slot. Creating a job or finishing admission does not prove
that a workflow succeeded. The review's `automatic_result` requires saved proof
of the exact generation-one execution or a final admission refusal. Missing or
superseded evidence stays outstanding, including after normal history retention;
investigate it before enabling further automatic work. Manual operator recovery
remains governed by its own native fences and quotas.

Inspect worker waiting reasons and pressure with `workflow stored recovery-policy --app SLUG --queue` and
inspect the corresponding initial review with `workflow stored failure-recovery-review
--app SLUG --review UUID`. Queue entries use the initial review UUID as both review
and action ID. Pending entries can be suppressed through the existing queue action
API, or cancelled through the initial-review decision command. Use the existing
policy pause/resume controls to stop and restart automatic work. Pausing also
blocks pending automatic native admission; it does not cancel an already admitted
run. Repeated proven failures pause the current policy and emit
`stored_workflow.recovery_policy_paused` to the captured app receivers.

This mode recovers generation zero once. It does not automatically continue into
later generations. Further recovery can use a separately configured
`automatic_retry` policy for saved failures. Native recovery job lifecycle
notifications remain the source of execution completion. First-recovery waiting
reasons are retained on reviews and shown in queue snapshots; existing health
incident monitors consume the combined queue. Initial recovery also emits
blocked and recovered queue notifications as described below. Repeated pause cycles after operator
resume emit distinct pause events.

Policy-change previews include a bounded initial-failure sample for this mode.
Use the failure inbox and individual previews to inspect additional candidates.

### Preview automatic first recovery before enabling it

Run `gregale workflow stored recovery-policy --app SLUG --file policy.json --preview`
with the proposed policy and its current `expected_version`. This command does
not update the policy, prepare reviews, submit jobs or schedule notifications.
Read-scoped credentials can use the preview; applying the file still requires
write permission and the unchanged expected version.

The `initial_failures` section separates three decisions for each captured failure:

- `eligibility`: policy filters and fresh safe-resume diagnostics.
- `preparation`: whether a review would be prepared, including duplicate pending
  recovery, retention quota, shared action budget and cooldown.
- `execution`: whether that prepared review would be submitted automatically,
  including the independent execution budget and cooldown, unresolved app work,
  account recovery-job quota and HTTP runtime availability.

For example, a qualifying failure can report `eligible`, `would_prepare` and
`hourly_execution_budget`: its review could be prepared, but submission would
wait. Preview policies report `not_enabled` for automatic execution. A disabled
proposal reports `policy_disabled` and neither prepares nor submits anything.
Replacing a paused policy clears the pause, but does not refund charges or free
an unresolved recovery slot; the preview models those replacement semantics.

Counts describe at most five retained initial failures. Matching workflow and
failure-status filters sort first, followed by ascending failure ID. `coverage`
is always `partial`, and `truncated` signals more retained failures beyond the
sample. Preparation and submission decisions simulate processing this sample in
order after sampled parent-job preparations: one predicted preparation can consume
cooldown or budget for the next candidate. Pressure fields show the snapshot
before simulation. Sample order is advisory and does not guarantee the worker's
next selected failure.

The memory and PostgreSQL previews include the same retained initial preparation
charges. Expired history is excluded from review quota as if it were pruned, but
previewing never performs that pruning. Unknown automatic outcomes continue to
occupy their slot even after normal retention. The actual worker rescans and
repeats policy, evidence, safety, budget and quota checks before submission and
native admission.

### Initial automatic recovery attention alerts

Subscribe an app webhook to `stored_workflow.recovery_queue_blocked`. For
`automatic_initial`, payloads include `initial_failure_id` and `state`:

| State / reason | Operator action |
| --- | --- |
| `blocked` / `evidence_unknown` | Inspect retained run and job evidence. Missing evidence keeps the execution slot occupied. |
| `blocked` / `terminal_outcome_not_saved` | Inspect the result recorder and retain proof for the exact resumed generation. |
| `blocked` / `execution_quota` | Inspect active recovery jobs across the account and free capacity. |
| `blocked` / `job_paused` | Inspect the job and resume it when safe. |
| `recovered` / `blocker_cleared` | Inspect current review/job progress. This means the blocker cleared; it does not prove success. |

Each alert includes `review_status_url`, `queue_url`, an `operator_action`, and
`job_url` when a job exists. `previous_reason` identifies the preceding blocker.
A changed actionable reason opens a new event; repeated worker polls with the
same reason stay quiet. Clearing and then reopening the same blocker produces
a new event ID. Normal budget/cooldown waits and running jobs do not open alerts.
Policy pauses use `stored_workflow.recovery_policy_paused`.

The worker records `automatic_block_reason`, `automatic_block_sequence` and
`automatic_block_notification` on the retained review. The combined queue exposes
the latest receipt as `blocked_notification`. `queued` means webhook ledger
admission, not successful delivery. Receivers are frozen for each transition;
`no_receivers` prevents historical replay to later subscribers. Deliveries are
historical and can arrive after a later change: deduplicate by `event_id` and
compare `observed_at`.

A clearance requires a fresh observation of admission, progress or a proven
result. Reads, policy changes, cancellation and expiry do not manufacture
clearance notifications. Notifications never authorize another recovery or
release an unresolved slot. Native recovery completion events retain their
existing meaning. Saved-failure retry payloads omit `state` and
`initial_failure_id` and retain their existing once-per-action behavior.

### Diagnose and repair missing initial recovery evidence

Inspect a submitted automatic first-recovery review:

```sh
gregale workflow stored failure-recovery evidence --app orders --review REVIEW_UUID
```

The read-only diagnosis reports the frozen run and expected generation, available
execution proof, a reason and `repairable`. It exposes no workflow input. Only
an exact retained generation-one terminal run with matching job and admission
history, verified timestamps and a missing saved result is repairable. Running,
unknown, superseded, missing-job, missing-finish-time and conflicting-result
cases remain blocked.

Save verified proof with a stable request UUID and operator reason:

```sh
gregale workflow stored failure-recovery evidence --app orders --review REVIEW_UUID \
  --repair --request-id REQUEST_UUID --reason 'Restore verified terminal proof'
```

Use the same request UUID and reason when retrying an uncertain response. An
identical request returns its retained receipt; changing either conflicts.
Repair never overwrites existing result evidence. If ordinary result capture has
already saved proof, diagnosis reports `already_recorded`; no repair is needed.
A review whose worker outcome is already final reports `already_confirmed`.

The review retains `evidence_repair` with request identity, actor, reason, time
and outcome, and an `evidence_repaired` audit event. Repair saves proof and
schedules the normal worker to re-evaluate; it does not release the slot,
create another recovery, refund budget or claim success itself. Existing policy
pauses and disablement remain in force. Resume or enable through the usual
policy controls when appropriate. A confirmed failed automatic result can still
trigger the normal policy failure pause; manual results leave its counters unchanged.

The HTTP endpoints are `GET /v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence`
and `POST /v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence/repair`.
Diagnosis requires app read access and MFA; repair requires deploy write and MFA.
Repair applies to submitted `automatic_initial` reviews and manually approved
`preview` initial reviews with expected generation zero. It can inspect
unresolved older policy versions but cannot reconstruct pruned history or
force-clear unknown evidence.

### Diagnose and repair automatic retry evidence

Automatic retries now have the same verified repair workflow as first recovery.
Use the review and action IDs from the recovery queue:

```sh
gregale workflow stored recovery evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID
```

Diagnosis is read-only and reports the child job, run, exact planned generation,
available execution proof, a reason and `repairable`. For example, a retry of a
saved generation-one failure must prove generation two. A generation-three live
run cannot substitute for missing generation-two evidence.

When diagnosis reports a repairable missing result:

```sh
gregale workflow stored recovery evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID --repair \
  --request-id REQUEST_UUID --reason 'Restore verified retry outcome'
```

Reuse the same request UUID and reason after an uncertain response. Identical
retries return the receipt; changes conflict. The retained review exposes
`evidence_repairs`, keyed by action UUID, with request identity, actor, reason,
repair time and verified outcome. The diagnosis also includes that action's
receipt. Existing result evidence is never overwritten.

Repair requires an applied automatic retry action, matching immutable plan,
parent job and position, child request identity, workflow/run/count, retained
admission and exact terminal generation with verified timestamps. Pending or
suppressed actions, pruned jobs/history, changed lineage, newer generations,
missing finish times and conflicting results remain blocked. Diagnosis exposes
no workflow input.

Proof repair is atomic with its audit receipt. It schedules normal worker
re-evaluation; it does not submit a child, release a slot, refund budget, reset
failure counters or resume a paused policy itself. The worker performs ordinary
outcome accounting and can still pause after proven failures. Unresolved retained
older actions can be repaired after policy version or mode changes, but current
policy controls remain in force.

The HTTP endpoints are
`GET /v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence`
and the same path with `/repair` for POST. Read access and MFA are required for
diagnosis; deploy write and MFA are required for repair. First recovery continues
to use `failure-recovery-evidence` and its existing endpoints.

### Repair parent evidence before an automatic retry starts

A pending retry can be blocked when the parent's saved result is missing. Select
its parent evidence explicitly:

```sh
gregale workflow stored recovery evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID --parent
```

Diagnosis reports `evidence_kind: parent`, the parent `job_id`, exact
`item_position`, run and required parent generation. For a planned generation-two
retry, this verifies generation one. It never substitutes a newer generation or
another item for missing evidence, and it exposes no workflow input.

When the exact retained parent terminal outcome is verifiable and its saved
result is missing:

```sh
gregale workflow stored recovery evidence --app orders \
  --review REVIEW_UUID --action ACTION_UUID --parent --repair \
  --request-id REQUEST_UUID --reason 'Restore verified parent outcome'
```

Repair is available only while the automatic action is pending and has no child.
The native parent item, frozen workflow/run/generation and retained admission
history must agree. Update and non-null finish times must be valid for that
admission. Unknown, superseded, missing-history, missing-item and conflicting
proof stays blocked. An existing result is never overwritten. The actual
verified outcome is saved; the operator cannot force a failed outcome to make
an old retry eligible.

The review retains `parent_evidence_repairs`, keyed by action UUID, independently
of child `evidence_repairs`. Both use the same audit receipt format. Retry an
uncertain parent repair with the same request UUID and reason to get its retained
receipt, even after the worker submits a child. Changing either conflicts. A
later child repair uses its own receipt and request identity.

Parent repair creates no child, spends no execution budget, releases no slot and
does not resume a paused policy. The normal worker must recheck the current
policy version, eligibility, cooldown, budget, quota and unresolved work before
submission. Paused policies, changed versions and exhausted budgets still block
execution. Retained old pending reviews can have proof restored, but restoration
does not restore their policy authority. Suppressed and applied actions cannot
perform a new parent repair.

HTTP clients use GET
`/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/parent`
and POST to the same path with `/repair`. Diagnosis requires read access and MFA;
repair requires deploy write and MFA. Omit `--parent` or use the existing evidence
endpoint to inspect the applied child's outcome.

### Follow retry evidence alerts to the correct repair

`stored_workflow.recovery_queue_blocked` retry events now include `state`,
`evidence_kind` (`parent` or `child`), `run_id`, `resume_count`,
`diagnosis_reason`, `repairable`, `evidence_url`, `job_url` and `cli_command`.
Use the diagnosis URL or command to inspect current proof before taking action.
When `repairable` is true, the reason is `terminal_outcome_not_saved`; append
`--repair --request-id REQUEST_UUID --reason 'Restore verified outcome'` only
after diagnosis confirms the exact terminal outcome. Unknown, superseded,
conflicting or missing history cannot be forced.

A pending action points to `recovery-retry-evidence --parent`; an applied action
points to the default child diagnosis. Detailed diagnosis changes produce a new
capture. Repeated unchanged observations stay quiet. Exact saved terminal proof,
a confirmed result or verified child execution progress can emit
`state: recovered`, `reason: blocker_cleared`. This reports cleared evidence
attention, not successful execution or permission to start another retry.
Pending parents also require the exact live generation to remain retained. A
saved result cannot clear a missing or superseded parent run. Policy pauses,
suppression, dismissal and version changes alone cannot clear it.

The action's `blocked_notification` is the latest receipt, with optional `state`,
`sequence`, `evidence_kind` and `diagnosis_reason`. Old receipts remain readable.
Each transition has a stable event ID and freezes its receiver set, including
`no_receivers`. Later subscribers do not receive old captures. Historical blocked
payloads remain unchanged even when delivery happens after evidence restoration.
All execution, policy and budget decisions still belong to the normal worker.

### Trace evidence repairs and alerts in stored workflow history

Use `gregale workflow stored history --app orders`, optionally filtered by
`--workflow`, `--run` and `--policy-version`, to follow evidence attention:

- `retry_evidence_blocked` preserves a captured parent or child diagnosis.
- `retry_parent_evidence_repaired` and `retry_child_evidence_repaired` identify
  the verified outcome, operator actor, reason and repair request UUID.
- `retry_evidence_cleared` preserves the later evidence clearance notification.

Entries include `evidence_kind`, `resume_count`, `request_id` or
`notification_event_id`, `diagnosis_reason`, `review_url`, `evidence_url` and
`job_url` when applicable. The CLI prints these fields; JSON/API/SDK clients use
existing recovery history endpoints and models. Run, action, review, policy and
job associations let you connect these events to execution results.

New events are captured atomically with their receipts, including notification
captures without receivers. Replayed repairs and unchanged blockers create no
extra event. Old repair receipts appear as retained observations when no matching
durable event exists and their source review is retained. New durable events
survive job/review pruning; old receipts cannot be reconstructed after their
review is deleted. Reads never
backfill events. Latest notification observations describe the retained snapshot;
blocked and cleared transitions are separate immutable events recorded from now
on. Evidence clearance does not mean workflow success.

Existing account scope, filters, fenced pagination, 30-day durable retention and
10000-event per-app cap apply. History capture spends no execution budget and
changes no admission, slot or policy decision.

### Inspect current evidence from the retry queue

`gregale workflow stored recovery-policy --app orders --queue` now displays current
parent or child evidence for returned retry entries. The API/SDK entry includes
an optional `evidence` object with `diagnosis`, `evidence_url` and `cli_command`.
The diagnosis identifies the exact run, generation, item, current reason,
repairability and observation time. Pending actions inspect parent proof; applied
unresolved actions inspect child proof. The inspection command selects `--parent`
when appropriate and performs no repair.

`wait_category` and `wait_reason` describe current pending policy controls or the
last saved worker wait. Budget and cooldown waits are separate from fresh proof:
a paused or budget-blocked action may still have repairable evidence. These fields
do not prove that all admission checks would pass. Worker evaluations and waiting
ages remain historical; reading the queue never refreshes them.

A missing or superseded live parent cannot be marked repairable by an older saved
result. Missing/expired review authority or changed plans return
`evidence_unavailable_reason`. Initial recoveries use their separate
`failure-recovery-evidence` command and omit the retry `evidence` object.

Inspection is bounded to the existing returned page and uses one consistent
read-only snapshot with the normal request timeout. It creates no child,
notification, repair receipt, budget charge or history event. Recheck the linked
diagnosis before repairing; the repair endpoint and worker independently recheck
proof, policy, quota and budgets.

### Inspect initial recovery evidence from the queue

Initial recovery entries in `gregale workflow stored recovery-policy --app orders
--queue` now include `initial_evidence`, alongside the retry-only `evidence`
field. Its `stage`, `diagnosis`, `evidence_url` and `cli_command` point to the
initial review and `gregale workflow stored failure-recovery evidence --app orders
--review REVIEW_UUID` inspection command.

A prepared review has no submitted recovery job. The diagnosis identifies its
planned target generation, reports `not_submitted_automatic_initial`, and sets
`repairable: false`. The CLI explicitly explains that evidence repair is
unavailable before submission. A submitted review uses the existing exact
resumed-generation proof checks and exposes current repairability, including
running, missing, superseded and conflicting proof.

Policy pauses and saved budget/cooldown waits remain separate from diagnosis.
Every diagnosis uses the queue observation time within the same bounded read-only
snapshot. Reading prepares or submits nothing, saves no outcome, emits no alert
or history event, and changes no policy, worker evaluation or execution charge.
Recheck the linked diagnosis before an explicit audited repair.

### Follow initial-recovery attention through alerts and history

Initial `stored_workflow.recovery_queue_blocked` events now include
`evidence_kind: initial`, `run_id`, `resume_count`, `diagnosis_reason`,
`repairable`, `evidence_url` and the matching `failure-recovery-evidence`
inspection command. Terminal proof can recommend audited repair only when the
existing exact-generation checks verify it. Job pause and execution quota alerts
retain their operational guidance.

Changed evidence diagnoses create a new capture; unchanged blockers stay quiet.
Only committed worker admission, execution or recorded-result progress can clear
attention. Policy changes and queue reads cannot. `state: recovered` reports
cleared attention, not workflow success. Each capture freezes its receivers,
including `no_receivers`; historical payloads never change after repair.

The shared `workflow stored history` timeline now records
`initial_evidence_repaired`, `initial_recovery_blocked` and
`initial_recovery_cleared`. Entries link the initial failure, review, run, exact
generation, job and repair request or notification event. Repair entries retain
the trusted actor, reason and verified outcome. Identical repair replay and
unchanged alerts add no event.

Older repair receipts appear as retained observations while their source review
exists; reads do not backfill durable events. New history capture is atomic with
receipts and survives review pruning within existing retention. Account scope,
workflow/run/version filters, fenced pagination, 30-day retention and the per-app
cap apply. SDK/receiver consumers should accept `initial` in the evidence-kind
enum and optional `initial_failure_id` in history. Apply the accompanying
migration before running the updated worker.

### Follow initial recovery lifecycle in shared history

`gregale workflow stored history --app orders` now records new initial-recovery
lifecycle transitions: `initial_recovery_prepared`,
`initial_recovery_submitted`, `initial_recovery_cancelled`,
`initial_recovery_expired`, `initial_recovery_stale` and
`initial_recovery_result`. Use workflow, run and policy-version filters to connect
these with the existing evidence repair and blocked/cleared events.

Preparation and decision events preserve the trusted actor, reason, frozen policy
version, initial failure and review. Submission adds the committed job link.
Result events use the existing automatic worker's verified outcome and outcome
reason; creating a job or clearing attention does not prove success. Generation
on a prepared event identifies the planned recovery target.

Identical preparation/decision replay and inspection reads append no event. Expiry
or staleness becomes history only when committed by a mutation, not when observed
by a read. The events are written atomically with their state change, remain after
source review/job pruning, and follow existing account scope, filters, fenced
pagination, 30-day retention and per-app cap. Older review events are not backfilled.
Preparations and decisions cover preview and automatic-initial reviews; final
results use the existing automatic worker. Apply the accompanying migration
before deploying the updated worker.

### Manually approved initial recovery completion

A preview-mode review approved with `failure-recovery-review --decision submit`
is observed after submission even if the policy is subsequently changed,
disabled or paused. Inspect `result` on the review or the durable
`initial_recovery_result` in stored workflow history for verified success, failure or
cancellation. The CLI review inspector prints the outcome and generation.
`automatic_result` remains reserved for automatic-initial execution.

Completion requires the exact job and generation-one saved execution evidence
(or a verified terminal admission refusal). Missing or unverified proof remains
unresolved and the submitted review survives normal retention pruning. Manual
observation does not spend automatic budgets, change automatic failure counters
or authorize additional retries. Use `failure-recovery-evidence` to diagnose or repair retained exact-generation
proof, as described below.

### Repairing manual initial recovery evidence

The existing `gregale workflow stored failure-recovery evidence --app APP --review REVIEW`
command diagnoses submitted preview-mode initial recoveries as well as automatic
initial recoveries. Add `--repair --request-id UUID --reason REASON` to save a
missing result when the retained job, generation-one admission and completed run
all match. The repair receipt and `initial_evidence_repaired` history event record
the operator and reason; replay the same UUID and reason to retrieve that receipt.

Repair wakes the manual completion observer even after the current policy is
changed, disabled or paused. The observer then records `result` and one durable
`initial_recovery_result`. Repair does not directly confirm completion, submit a
retry, change automatic budgets or resume the policy. Missing jobs/admission,
missing completion timestamps, superseded generations and conflicting saved
proof remain non-repairable. An already confirmed review reports
`already_confirmed`; an unsupported preview review reports
`not_submitted_manual_initial`.

### Attention for manually approved initial recoveries

Submitted preview-mode reviews expose `attention`: the latest committed observer
reason, diagnosis, unresolved `block_reason`, observation timestamp, evidence
URL, CLI command and operator guidance. Review reads leave it unchanged; use
`failure-recovery-evidence` for a fresh diagnosis. The review inspector prints
this information alongside the eventual verified `result`.

Subscribe to `stored_workflow.recovery_queue_blocked` for actionable missing proof,
unpersisted terminal outcomes or a paused manual job. Unchanged polls stay quiet;
a changed evidence diagnosis gets a new event. Recipients are frozen when an
event is captured, and `no_receivers` events do not replay to later subscribers.
Manual alert `queue_url` links to the initial review list.

A verified result or exact-generation execution progress can emit a recovered
transition. A recovered alert clears an evidence blocker and does not imply
successful execution. Policy changes, disablement, automatic pause, admission
waits or unverified progress cannot force clearance. Shared history retains
`initial_recovery_blocked` and `initial_recovery_cleared`, including the observer
actor, frozen version and notification event ID. Tracking continues independently
of current policy settings and does not alter automatic budgets or failure
counters, unpause a policy or authorize another retry.

### Independent recovery polling

Automatic execution and manual initial-recovery observation use separate bounded
scheduler passes. A large manual backlog no longer consumes automatic execution
slots. The scheduler also gives manual monitoring its own allowance when
automatic work remains continuously available.

An error ends only its affected pass; the other pass and downstream recovery
work still run within the shared scheduler context. Database outages or parent
context cancellation can still affect both. Manual completion, diagnosis,
attention and repair behavior remain independent of current policy settings.

### Manual observation failure retries

A deterministic data error while observing a submitted manual review is deferred
so later reviews can progress. Inspect `observation_failure` on the review or
`failure-recovery-review` output for the safe error code, consecutive attempts,
last failure time and next retry time. Retry delay doubles from one minute up to
30 minutes; successful observation clears this metadata. Attempts saturate at 32.
Reads do not retry work, and a retry does not grant another execution or prove a
final result. Failed PostgreSQL observation writes are rolled back before the
failure and next retry time are saved together.

Database outages, cancellation and unknown errors still end the affected pass.
Automatic execution errors retain their existing handling. Existing exact-proof
evidence repair can wake manual observation through its persisted due clock.

### Automatic policy polling failure retries

Inspect `automatic_execution.polling_failure` on a recovery policy or automatic
queue response for a safe error code, consecutive attempts, last failure time and
next retry time. The policy CLI prints the error and retry time. Deterministic
policy/data errors roll back execution writes, then defer only that policy so
other apps can progress. Retry delay doubles from one minute to at most 30 minutes;
attempts saturate at 32. Successful polling, a policy update or explicit operator
resume clears this metadata. Read-only inspection does not retry work.

Polling failures do not increment execution failure counters, consume budgets,
submit another job or grant execution authority. Database outages, cancellation,
transaction failures and unknown errors still end the affected pass. Apply
migration `20261014092444724` before deploying the updated automatic worker.

### Persistent polling failure alerts

Subscribe an app webhook to `stored_workflow.recovery_polling_status` to receive a
`failed` alert after three consecutive deterministic polling failures, followed
by `recovered` when polling succeeds. With normal backoff, the third failure is
observed after about three minutes. Unchanged code and policy version stay quiet;
changing the retry time or attempt count alone does not send another alert.

The payload includes the app, manual-review or automatic-policy scope, safe error
code, attempts, next retry time and inspection URL. `monitoring_only` is true:
recovered confirms successful polling, even if execution is still pending or no
new job was submitted. Policy updates, disablement or operator resume do not
prove recovery; a subsequent successful poll must confirm it.

Inspect `polling_notification` on the review or policy for the last frozen receipt,
including event ID and recipients. The CLI prints its state and code. `queued`
means durable outbox admission, while `no_receivers` never replays to subscribers
added later. New transitions use current recipients. Stored workflow history retains
`polling_failed` and `polling_recovered` with their frozen receipts. Apply migration
`20261014092444767` before deploying the worker.
