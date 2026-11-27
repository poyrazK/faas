# Run task pipelines and batches

Gregale Run can execute a bounded graph of version-pinned tasks after the client
exits. This is a default-off preview. The service that reasons, schedules agent
turns and manages conversations remains the agent service; Run executes its tools
with disposable isolation, contracts, artifacts, attribution and budgets.

The [customer examples](../examples/gregale-run/README.md) include a complete
parallel reporting pipeline, budget-refusal recovery and Agent continuation.

Publish task versions first using [Run tasks](run-tasks.md), then submit this JSON
to `POST /v2/execution-workflows` or use
`gregale sandbox workflow run --managed --manifest pipeline.json`:

```json
{
  "workflow_id": "report-customer42-001",
  "version": "v1",
  "max_parallel_steps": 2,
  "pipeline": {"total_timeout_ms": 300000},
  "steps": [
    {
      "label": "prepare",
      "task": {"name": "prepare-report", "version": 3, "input": {"month": "2026-11"}},
      "retry": {"max_attempts": 2, "backoff_ms": 1000, "safe_to_repeat": true}
    },
    {
      "label": "render",
      "task": {"name": "render-report", "version": 2},
      "input_from_previous_result": true,
      "artifact_inputs": [{"from_step": "prepare", "name": "report.csv", "path": "report.csv"}]
    }
  ]
}
```

The producer task must declare `report.csv` in its published `output_files`.
Stored artifacts arrive under `context.input_dir/report.csv`; inline artifacts
arrive in the consumer's bundle. Task result contracts must pass before their
results or artifacts are consumed. Explicit dependencies support parallel branches
and fan-in with `include_dependency_results`; `continue_independent` retains the
managed DAG's failed-branch behavior.

For independent invocations of one version, replace `steps` with `batch`:

```json
{
  "workflow_id": "reports-001",
  "version": "v1",
  "max_parallel_steps": 2,
  "batch": {
    "name": "render-report",
    "version": 2,
    "items": [
      {"label": "north", "input": {"region": "north"}},
      {"label": "south", "input": {"region": "south"}}
    ]
  }
}
```

Submit exactly one of `steps` or `batch`, with at most 16 unique labels. A task step
sets `task`; a source step sets `request`. The server owns workflow and receipt
labels. A task name always requires its exact numeric version. Definitions are
sealed into the plan, and GET returns the catalog ID and digest. Retiring a task
version blocks steps and retries not yet admitted.

Retries default to one attempt. Multiple attempts require an explicit
`safe_to_repeat: true` declaration and allow at most three attempts. Only scheduler
`restore_failed`, `execution_transport_failed` and `lease_expired` failures retry.
Backoff starts at `backoff_ms` (1–60 seconds), doubles and caps at 60 seconds. Guest
errors, OOM, timeouts, cancellation and contract failures finish the step. External
side effects may have happened before an infrastructure failure; use destination
idempotency keys when repeating a call could matter.

Each physical attempt has its own Run ID, task invocation, customer attribution,
budget reservation and settled usage. Concurrency budget limits queue later steps without consuming attempts; daily invocation or compute budget exhaustion refuses further admission.
Capacity waits do not consume retries. File capabilities are checked again for each attempt; a consumed single-use grant cannot authorize another Run. Use an owned ready file for reusable inputs. The one-hour default pipeline deadline
includes capacity waits and backoff; its maximum is 24 hours. Every Run deadline is
clipped to the remaining pipeline deadline, so scheduler enforcement survives an
apid outage. Live key-family bindings and budget policies are rechecked for every
attempt.

Use `gregale sandbox workflow <workflow-id> --json` or
`GET /v1/execution-workflows/{workflow_id}` for step pins and attempt lineage.
Use `gregale sandbox workflow cancel <workflow-id>` or
`POST /v1/execution-workflows/{workflow_id}/cancel` to close the admission fence and
request cancellation of active Runs. Cancellation completes after those Runs
settle. For wire compatibility, the managed status is `failed` with
`pipeline.cancel_requested=true` and `error="pipeline cancelled"`.

Replaying the same workflow ID and normalized request recovers the original job,
even when publication gates are off or a task has since retired. A different plan
with the same ID returns 409. Runs-only credentials see and cancel only their
stable key family's workflows. `--dry-run` validates manifest shape locally;
catalog access, compiled contracts and budgets are checked by the service.

Operators enable `FAAS_EXECUTION_PIPELINES_ENABLED=1` alongside the existing Run
API, V2 and task gates. `GET /v1/executions/capabilities` exposes `task_pipelines`.
Apply the migration before deploying the capable control plane. Keep at least one
capable worker until active pipelines drain; older workers skip these jobs. Turn
the writer gate off to stop new admissions while allowing observation,
cancellation and deadline cleanup. This increment changes orchestration and
admission only; it does not change the guest or VM lifecycle protocol.

## Whole-pipeline completion

Newly completed pipelines expose `managed[].pipeline.completion` on
`GET /v1/execution-workflows/{workflow_id}`. The immutable v1 manifest contains
`completion_id`, `workflow_id`, `outcome`, `finished_at`, `receipt`, `usage` and
ordered `steps`. Outcomes distinguish `succeeded`, `failed`, `cancelled` and
`deadline_exceeded`. Step outcomes additionally identify contract failure,
blocked dependencies and work that never started. Every physical attempt retains
its Run ID, status, receipt, host usage and artifact references.

This is a metadata contract: fetch actual results and artifact bytes through the
referenced authorized Run receipts. Their existing retention/expiry still applies.
The manifest does not retain source, inputs, stdout, stderr, result bytes, artifact
bytes or file capabilities. Aggregate wall/CPU time sums every attempt; peak memory
is the maximum individual attempt peak. These measurements are not an invoice or
simultaneous residency. The serialized manifest is bounded to 1 MiB.

Account webhook receivers may subscribe to `execution.pipeline.finished`. Its
payload contains `event_id` (the completion ID), `workflow_id`, `outcome`,
`finished_at` and `receipt_url`. The terminal transition captures delivery in the
existing signed outbox; retries, dead-letter inspection and redrive work as for
`execution.finished`. Individual Run events retain their existing behavior.

## Durable Agent continuation

The RAP operation `gregale_pipeline` accepts `steps` or `batch`, plus optional
`pipeline`, `max_parallel_steps` and `failure_policy`. It requires the owning app's
workload token or deploy credential and a live thread callback URL. Every task
version must list that endpoint in its published `agent_endpoint_ids`. The complete
RAP invocation, including its callback URL, retains the existing 8 KiB limit.

```json
{
  "operation": "gregale_pipeline",
  "id": "report-call-42",
  "call_id": "provider-call-42",
  "group_id": "thread-42",
  "callback_url": "<issued thread callback URL>",
  "arguments": {
    "batch": {
      "name": "render-report", "version": 2,
      "items": [{"label": "north", "input": {"region": "north"}}]
    }
  }
}
```

The platform owns workflow identity and customer attribution. Agent arguments
select delegated task versions, [private step uploads](#private-uploads-for-agent-steps),
and artifact handoffs between tasks. Every attempt rechecks live
task delegation and endpoint budget bindings. Persist the same logical call ID
before dispatch and reuse it after a lost acknowledgement. JSON object key order
is canonicalized for Agent submission replay, including after a JSONB round trip.

An accepted call acknowledges with an empty 200. No Agent compute is held while
its pipeline runs. Terminal completion publishes one logical tool result through
the durable inbox, carrying `completion_id`, `workflow_id`, `outcome` and the scoped
`receipt` reference. Read `GET /v1/agent-tools/pipelines/{completion_id}` with the
app credential for `{id, completion}` (completion is absent while still pending).
The manifest links to app-scoped Run receipts/artifact downloads. Revoking the
original thread grant or deleting the endpoint stops delivery and these reads;
use explicit workflow cancellation to stop execution.

The stateless Node starter exports `GregalePipelineTools({platform})` alongside
`GregaleTaskTools`; `platform.pipelineReceipt(completionID)` fetches the manifest,
and existing `taskReceipt`/`taskArtifact` helpers fetch its linked outputs.
Delivery is at least once, with stable inbox identities for deduplication across
restarts. Applications must also deduplicate their own external effects.

Writer gates stop new admissions while retaining observation, exact submission
replay and completion delivery. Keep capable workers until callback pipelines
and pending completion obligations have drained before rolling back binaries.
Already-terminal legacy pipelines may have no manifest; no output backfill occurs.

## Pipeline history in the dashboard

Open **Run pipelines** from **Jobs & queues** to browse the signed-in account's
pipeline receipts. A pipeline page shows live step status, terminal attempts,
and links to the parent of a rerun. It also downloads retained artifacts from
the attempts that produced them. App-owned deliveries continue to use their
separate delivery receipts and retention policies.

**Usage** counts each newly admitted Run once, including retry attempts. Reused
steps do not add execution time or output bytes to the child pipeline. The
execution totals come from the Run usage ledger as attempts finish. The wait
column measures admission to the recorded start of each Run, and times for
parallel Runs can overlap. Submission to completion measures elapsed pipeline
time. Run output times show when the producing attempt recorded its artifacts.
App-owned delivery can finish later. The platform does not currently expose
settled compute or storage charges per pipeline.

For an unsuccessful pipeline that belongs to the account session, select the
original JSON manifest and choose **Review rerun**. Review which steps will run,
which can reuse their existing outputs, and which require approval to repeat.
The dashboard uses the same child workflow UUID for retries from the parent
page. The Run API verifies the manifest, task versions and retained outputs
before admission. Pipelines started with an API key use the original key
family from the app or CLI. Agent callback pipelines use a new delegated tool
call from the original Agent.

## Live progress

New submission receipts include `id`, the pipeline ID used for observation and
reruns. `GET /v1/execution-pipelines/{id}/events?after=0` returns metadata events,
`next_sequence`, `terminal` and the latest `progress` snapshot. Send
`Accept: text/event-stream` for SSE and reconnect with `Last-Event-ID` or `after`.
The explicit `after` query takes precedence. A future or negative cursor is an
error. Stopping observation never cancels execution.

```sh
gregale sandbox workflow watch PIPELINE_ID --after 0 --json
```

Each observed transition gets a durable sequence. Steps distinguish queueing,
execution, retry backoff, blocked dependencies, contracts and reused work. Reasons
also identify concurrency budgets, capacity waits, parallel limits and disabled
admissions. Run IDs link to the existing execution event streams for logs. The
journal contains no code, input, output bytes or log text. Worker polling can miss
short physical transitions; use the Run streams for finer detail.

The journal retains its latest 256 events. A page sets `gap` if earlier events
were discarded, and includes the current snapshot for resynchronization. Advance
using `next_sequence`; `terminal` becomes true after the retained journal is fully
read. Repeated unchanged observations do not append events. Legacy pipelines can
have no journal. Reads remain available with pipeline writers disabled.

Go `WatchExecutionPipeline`, Node `watchExecutionPipeline`, and Python
`watch_execution_pipeline`/`awatch_execution_pipeline` expose resumable watches.
Apps with a live original Agent thread grant can read the same metadata through
`GET /v1/agent-tools/pipelines/{id}/events`; the stateless Node platform helper is
`pipelineEvents(id, after)`. App callers use their existing scoped task receipt
methods for outputs. Revoking the grant suppresses subsequent progress reads.

## Explicit failed-branch reruns

A rerun creates a **new workflow and receipt**. Supply the original manifest and
a distinct workflow ID. The server verifies the original normalized submission,
recompiles the exact task versions and checks their immutable pins. Original
source/input plans remain erased after terminalization.

```sh
gregale sandbox workflow retry PARENT_PIPELINE_ID report-recovery original.json \
  --repeat-steps render --json
```

The API is `POST /v2/execution-pipelines/{id}/reruns` with
`{workflow_id, original, repeat_steps}`. Failed, blocked and unstarted steps run
again, together with all affected descendants. A successful fan-in that previously
observed a failed branch is affected too. Every previously started affected step
must appear in `repeat_steps`; an incomplete or extra acknowledgement returns
409 with the required list and admits no child. This is explicit permission to
repeat work, not a guarantee against duplicate effects at an external provider.

Other successful steps are reused. Completion metadata pins their output
fingerprints. Reuse verifies result/artifact integrity and original key-family
ownership, and is limited to 24 hours after the producing Run finishes. Stored
artifacts must remain ready, unexpired and not revoked. Continuation checks reuse
again; ordinary file admission independently checks each artifact handoff. If a
required output is unavailable, start a fresh pipeline instead.

The child records `rerun.parent_id` and its reused Run references; its completion
steps expose `reused_run_id` without inventing a physical attempt. Reused usage is
not counted again. Every new attempt still uses the live task, file, customer
budget and cancellation fences. The original receipt stays immutable. Single-use input grants are not renewed;
use owned ready files for inputs that must survive repeated execution.

Reusing the same child workflow ID and intent recovers the original child before
catalog/output availability and writer checks. Changed intent returns 409. A
later rerun can use a failed child as its parent, resubmitting the same original
manifest; the service supplies that parent's workflow identity when verifying it.
This API requires the original key family. Agent callback pipelines require a new
delegated tool invocation and do not inherit an old conversation callback.

Apply the progress migration before deploying the new control plane. Capable
workers must remain while progress-enabled pipelines drain: a claim fence prevents
older workers from executing plans whose reuse metadata they do not understand.

## Private uploads for Agent steps

A trusted backend issues an upload grant through the
[Run file grant API](run-tasks.md#delegate-an-uploaded-file-to-one-tool-call).
Include the endpoint, thread, logical call ID, task name and version, guest
path, and `step_label` in the grant request. Use the exact label from an
explicit `steps` array. Attach the opaque token when dispatching the call.

```json
{
  "steps": [
    {
      "label": "validate",
      "task": {"name": "sales-report", "version": 1, "input": {"csv_file": "orders.csv"}},
      "file_grants": ["<grant scoped to validate>"]
    },
    {
      "label": "package",
      "task": {"name": "package-report", "version": 1},
      "depends_on": ["validate"],
      "artifact_inputs": [{"from_step": "validate", "name": "report.html", "path": "validated.html"}]
    }
  ]
}
```

The named tasks must already be delegated to the endpoint. The first step receives
the uploaded CSV. The second receives the declared report artifact. Up to four
upload grants fit across one Agent pipeline call, subject to the existing RAP
envelope and Run file limits. The backend keeps grants in private dispatch
storage and reuses them verbatim after an uncertain acknowledgement.

A token must be valid when submitting a new call. The sealed plan pins the upload
generation and path. Each attempt checks that the same upload is still ready
before admission. Revoking an upload blocks later attempts that need it.
Admitted Runs keep their existing file lease. Exact submission replay recovers
the original pipeline after token expiry or upload revocation.

Apply the scoped file claim fence migration and deploy the updated pipeline
workers before using these uploads. Settle active scoped-file pipelines before
removing their claim fence.
