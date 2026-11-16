# Typed durable job pipelines (SDK preview)

Compose typed Jobs using Gregale's existing code-first workflows. A pipeline
submits each registered job stage through an application-owned durable receipt,
observes native Jobs on durable wakes, and records verified results before the
next stage. It uses the existing workflow journal, deployment pinning, retry
policy and sleep operations. It introduces no new orchestrator or platform API.

The CSV example runs three native stages: validate the uploaded CSV, import its
rows, and generate a rejection report. Node and Python can replay the same
workflow journal and use the same PostgreSQL receipts. Completed stages replay
their recorded results; already accepted jobs are never submitted again.

## Contracts and authoring

Use the existing portable JSON contract format for the pipeline's input/output
and each stage. `gregale-job-types CONTRACT.json typescript|python OUTPUT`
generates both languages, including runtime validators. Only the supported
schema subset is accepted. The generated `JobContract<I,O>` works for either
a Job or the pipeline envelope. Existing workflow and Job callers are unchanged.

```ts
import {defineJobPipeline} from '@gregale/sdk-node/preview';
import {serveWorkflows} from '@gregale/sdk-node/workflows';
import {contract} from './pipeline-generated.js';

const pipeline = defineJobPipeline(contract, {
  scope, store,
  jobs: {
    validate: {contract: validateContract, job: validateJob, jobStore},
    import: {contract: importContract, job: importJob, jobStore},
    report: {contract: reportContract, job: reportJob, jobStore},
  },
  requestTimeoutMs: 10_000,
  pollDelayMs: 5_000,
  maxPolls: 120,
}, async (input, wf) => {
  const validated = await wf.job('validate', [{id: input.import_id, value: input}]);
  const imported = await wf.job('import', validated.results[0].result.rows);
  const rejections = imported.results.filter(r => r.result.rejection === 'invalid_email')
    .map(r => r.input_id);
  const report = await wf.job('report', [{id: input.import_id,
    value: {import_id: input.import_id, owner_id: wf.ownerId, rejections}}]);
  return {report: report.results[0].result,
    saved: imported.results.filter(r => r.result.saved).length, rejected: rejections.length};
});

export const POST = serveWorkflows([pipeline.definition]);
```

Construct clients, job definitions, stores and configuration outside workflow
invocations. Inside the handler, await `wf.job` and use deterministic value
mapping. Time, environment reads and network calls belong in job handlers. Each
registered stage name is used once per invocation. Its generated inputs and
outputs remain checked. Stage results use the portable shape
`{run_id, results: [{input_id, index, attempt, result}]}`; only native successful
tasks with verified typed results advance the pipeline. Failure stops downstream
stages and exposes a stable `pipeline:<code>` failure.

Python uses the same receipt/journal format, seconds for durations, and an
explicit typed definition to preserve type inference across heterogeneous stages:

```py
from faas_sdk import JobInput
from faas_sdk.preview import JobPipeline, JobPipelineContext, PipelineJob
from faas_sdk.workflows import serve_workflows

validate = PipelineJob(contract=validate_contract, job=validate_job, job_store=job_store)

async def run(value: Input, wf: JobPipelineContext) -> Output:
    validated = await wf.job("validate", [JobInput("source", value)], definition=validate)
    return map_output(validated.results[0].result)

pipeline = JobPipeline(contract, scope=scope, store=store,
    jobs={"validate": validate}, run=run)
app = serve_workflows(pipeline.definition)
```

## Durable submission and recovery

Install [the application schema](../sdk/request-contracts/job-pipelines.sql)
explicitly. The SDK never runs DDL at startup. `postgresPipelineJobStore` and
`PostgresPipelineJobStore` require one acknowledged **autocommit** statement per
query, using the same primary database. Do not use a business transaction or a
lagging read replica. The Python adapter takes PostgreSQL `$n` placeholders;
the example shows a parameterized psycopg adapter preserving repeated arguments.

An intent binds the account, endpoint, app, workflow run, application owner,
stage, contracts, settings and exact input manifest. The SDK commits it before
Jobs submission and records the accepted checkpoint separately. Input values
are represented by digests in this ledger. A repeated driver reads that intent
and never POSTs another Job while its acceptance is uncertain, including after
the native Jobs idempotency window expires. The supplied `jobStore` must match
the job's content-addressed store; the managed file JobStore satisfies this.
Concurrent SDKs may serialize equivalent values differently; the first durable
intent owns the exact native manifest, while canonical values/settings hashes
reject different logical inputs. Job submission also guards the bound contract
before dispatch, so a misconfigured stage cannot invoke another typed handle.

Lost store acknowledgements and driver crashes are safe to retry through the
existing workflow retry policy. An intent without a checkpoint remains visible
as `submission_unresolved` while durable observation continues. If acceptance
is known externally, authorize the owner and call:

```ts
await pipeline.reconcile(workflowRunId, 'import', inspectedJobRunId, ownerId,
  {timeoutMs: 10_000});
```

Reconciliation only reads native Jobs and adopts a checkpoint after matching
the exact saved manifest, account, endpoint and contract. It never submits work.
Its next durable observation sees the reconciled receipt. Keep receipts for as
long as the workflow can be redelivered; deleting them permits new submission.
Job handler side effects remain at least once: use stable input IDs, unique
constraints or external idempotency keys as in the CSV example.

## Start, attach, inspect and cancel

An application server creates an isolated `createJobPipelineClient` (Python:
`JobPipelineClient`) with its backend credential, scope, contract and the already
authorized application `ownerId`. The scope contains `base_url`, `account_id`,
`app_id` and `app_slug`. Account/app ownership is checked before starting work;
run reads verify the envelope, owner, input digest and contract.

```ts
const pipelineClient = createJobPipelineClient({
  baseURL, token: backendToken, scope, ownerId: session.userId, contract,
});
const run = await pipelineClient.start(input,
  {timeoutMs: 10_000, idempotencyKey: submissionKey});
save(run.checkpoint); // identities/digests, no credential
const attached = await pipelineClient.resumeById(run.checkpoint.run_id, {timeoutMs: 10_000});
const inspection = await attached.inspect({timeoutMs: 10_000});
// inspection.failedStep and inspection.failure identify SDK failures without raw errors.
const output = await attached.result({timeoutMs: 10_000});
```

Supply a stable submission key for the same input. Start has no transparent
mutation retries; after uncertain acceptance, reattach by the inspected run ID
or explicitly repeat the same key/input under the native retained-key contract.
Every operation requires a bounded deadline. AbortSignal/async task cancellation
stops local work and fences late callbacks. It does not cancel remote execution.

`resumeById`/`resume_by_id` attaches for reading; it does not restart execution.
Native **failed code-first workflows cannot currently be continued**. Inspect
them and start a deliberately reviewed new run when needed. New runs have new
step identities, so that choice can produce new job effects. This SDK adds no
automatic retry of an entire failed pipeline.

`run.cancel` cancels native orchestration. Already accepted Jobs continue
independently; inspect their receipts and use the existing typed Job cancellation
API when your application also wants them cancelled. Cancellation is not a
rollback of database writes or files. This first slice covers account-owned runs;
tenant-scoped workflow continuation is not exposed by this typed client.

## Artifacts, progress and limits

Large stage outputs belong in managed files. Pass a `PipelineArtifact` containing
scope, owner, key, optional immutable version, byte count and SHA-256. Authorize
the owner before calling `readPipelineArtifact`/`read_pipeline_artifact`. Reads
check scope, bytes and digest, even when an immutable version is available.
Checkpoints and workflow journals contain no signed download URL.

`jobPipelineLiveSnapshot`/`job_pipeline_live_snapshot` adapts native workflow and
Job state to the existing live view contract. Use it in an authenticated snapshot
loader with the existing `createLiveJobBridge`, private per-user tickets and
React/agent consumers. Pipeline success comes from the native workflow; its
report-ready callback must additionally read/verify the owned report. Native Job
success alone cannot establish pipeline completion. Each helper operation has
one total deadline; cancellation of observation leaves the pipeline running.

Defaults: 100 polls per stage, 1-second durable sleeps, 900 SDK operations, 64 KiB
of recorded values, and 10-second individual external-operation deadlines.
Polls are configurable up to 150 per stage; operations up to 950; recorded values
up to 256 KiB. These local budgets leave room within the native 1,000-operation,
1.5 MiB journal and 960 KiB per-response limits. Choose poll delay and workflow
timeouts for the expected job duration. Configure the native code-handler timeout
above the SDK's external-operation deadline. Large batches may hit the journal
budget before the 1,000-input cap; use artifact-shaped results for large data.

See [the CSV pipeline](../examples/sdk-csv-jobs/README.md#durable-three-stage-pipeline-preview).
Local acceptance uses real PostgreSQL and Node/Python processes with protocol
fixtures for scheduling, workflow dispatch and storage. It does not establish
deployed scheduler, storage or plan acceptance. Existing Jobs, workflow, storage
and preview account/plan gates remain in force.
