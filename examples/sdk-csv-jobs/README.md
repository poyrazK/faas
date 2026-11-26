# Typed CSV imports with Node and Python

This example accepts UTF-8 `row,email` CSV, submits one typed job input per row,
reports progress, and serves a downloadable CSV of rejected rows. A transient
failure can be replayed by stable input ID while successful inputs stay complete.
Either SDK can resume the other's checkpoint. PostgreSQL primary keys make
duplicate task delivery safe for this example's inserts.

Build/install the SDK from this checkout using its README installation steps.
Then install example dependencies and initialize an isolated database:

```sh
npm --prefix sdk/node run build
npm --prefix examples/sdk-csv-jobs ci
python -m pip install -e 'sdk/python[realtime]' -r examples/sdk-csv-jobs/requirements.txt
psql "$DATABASE_URL" -f examples/sdk-csv-jobs/schema.sql
```

`contract.json` is the schema source of truth. Both generators can emit either
language; regenerate after changing a job name or schema:

```sh
node sdk/node/dist/job-types.js examples/sdk-csv-jobs/contract.json typescript examples/sdk-csv-jobs/generated.ts
python -m faas_sdk.job_types examples/sdk-csv-jobs/contract.json python examples/sdk-csv-jobs/generated.py
```

For cloud execution, configure an existing Jobs template whose image runs
`worker.mjs` or `worker.py`, with this matching contract, `DATABASE_URL`, SDK and
driver dependencies. `GREGALE_JOB_STORE_MODULE` names your application's module
exporting a JobStore called `store`. Its `put` stores immutable bytes and returns
an `obj://`, `s3://` or `gs://` URI; `get` reads them. Use its own storage
credentials. Managed obj results can be downloaded by the SDK using native signed
requests; readers of external s3/gs results need a matching store adapter. The
local file store is exclusively for local tests and does not upload to S3.

Set `FAAS_API` and `FAAS_TOKEN` for the SDK's Jobs API access; managed artifact
downloads additionally require the server's storage read permission. Set
`GREGALE_IMPORT_APP_TOKEN` separately for this demonstration API:

```sh
export GREGALE_IMPORT_STATE=/private/path/imports
export GREGALE_IMPORT_APP_TOKEN=your-application-token
node examples/sdk-csv-jobs/api.mjs

curl -H "Authorization: Bearer $GREGALE_IMPORT_APP_TOKEN" \
  --data-binary @rows.csv http://localhost:8080/imports/my-import
curl -H "Authorization: Bearer $GREGALE_IMPORT_APP_TOKEN" \
  http://localhost:8080/imports/my-import
curl -H "Authorization: Bearer $GREGALE_IMPORT_APP_TOKEN" \
  http://localhost:8080/imports/my-import/rejections.csv
```

This is a single-operator demo; connect the routes to your application's user
authorization for a multi-user product. A repeated import ID with different CSV
bytes is refused. The API saves intent before submission and refuses automatic
resubmission when acceptance is uncertain. Inspect/recover with the same key
within the server's 24-hour window. The rejection report describes validation
rejections; inspect job progress/results for task failures separately.

The private checkpoint at `$GREGALE_IMPORT_STATE/my-import.json` contains only
identities and digests. Use either driver after restarting:

```sh
export GREGALE_JOB_CHECKPOINT="$GREGALE_IMPORT_STATE/my-import.json"
python examples/sdk-csv-jobs/import_job.py wait
node examples/sdk-csv-jobs/import.mjs results

export GREGALE_JOB_SUBMISSION_KEY=my-import-replay-1
export GREGALE_JOB_REPLAY_CHECKPOINT=/private/path/replay.json
python examples/sdk-csv-jobs/import_job.py replay
export GREGALE_JOB_CHECKPOINT="$GREGALE_JOB_REPLAY_CHECKPOINT"
node examples/sdk-csv-jobs/import.mjs wait
node examples/sdk-csv-jobs/import.mjs results
```

The drivers also accept `submit rows.csv` with an explicit
`GREGALE_JOB_SUBMISSION_KEY`, and `cancel` for remote cancellation. A saved
checkpoint is never overwritten by a new submit/replay. For CSV, input IDs use
`GREGALE_IMPORT_ID` or a stable digest of the file's contents. Set
`GREGALE_CSV_FAIL_ROW=2` before submitting to inject one durable transient failure
on row 2; replaying that same input then succeeds. Expected invalid emails produce
typed rejection results, rather than a failed task. Application writes already
committed are retained after cancellation.

Local integration requires PostgreSQL binaries, the SDK's Node development
dependencies, and the Python example dependencies including the realtime extra:

```sh
GREGALE_LOCAL_PG_BIN=/path/to/postgresql/bin \
GREGALE_LOCAL_PYTHON=/path/to/sdk-venv/bin/python \
node --test examples/sdk-csv-jobs/local.test.mjs
```

It starts isolated PostgreSQL, real Node/Python worker processes and the API, and
uses a fixture control plane to supply native task environments. It verifies both
SDKs across failed-only replay, cross-language resume, rejection downloads and
duplicate delivery. The deployed equivalent is `TestSDKTypedBackgroundJobs` in
`tests/journeys`, enabled only with an isolated API/token, `GREGALE_E2E_NODE_SDK`
and `GREGALE_E2E_JOB_CONTRACT` matching a configured example job with managed obj
result storage. Local tests do not establish deployed scheduler acceptance.

## Browser file imports (preview)

`file-api.mjs` and `file-index.html` provide an automatic workflow: authenticated
application record → resumable upload → native storage event → typed job →
private live progress → stored rejection report. The browser never calls
`/start` or polls `/progress`. Both SDKs
share a PostgreSQL submission ledger and the managed JobStore.

Configure an existing app and bucket with storage preview access and read/write
grants. Set bucket CORS to allow your application origin, PUT, and the returned
Content-Type header, exposing ETag and Content-Length. Configure the existing
`csv-import` Jobs template with the same database and SDK, and these worker vars:

```sh
export FAAS_API=https://your-gregale-api
export FAAS_TOKEN=your-backend-token
export GREGALE_FILE_APP=imports
export GREGALE_FILE_BUCKET=your-bucket-uuid
export GREGALE_FILE_ACCOUNT_ID=the-bucket-owning-account-uuid
export GREGALE_REALTIME_ENDPOINT_ID=your-private-32-hex-realtime-endpoint
export GREGALE_REALTIME_URL=wss://your-app/__gregale/realtime/your-endpoint
# On workers, prefer a credential scoped to this app/bucket:
export GREGALE_FILE_TOKEN=your-worker-storage-token
export GREGALE_IMPORT_STATE=/private/path/file-imports
export GREGALE_FILE_USERS='{"alice":"replace-with-alice-app-token","bob":"replace-with-bob-app-token"}'
psql "$DATABASE_URL" -f sdk/request-contracts/file-pipelines.sql
npm --prefix sdk/node run build
node examples/sdk-csv-jobs/file-api.mjs
```

Open `http://127.0.0.1:8080`, enter Alice's application token and choose a CSV.
The backend credential stays on the server. Replace the static token map with
your normal application authentication before using the example as a product.
The demo listens on loopback; production hosting also needs TLS. This file flow
accepts up to 1 MiB per CSV and generates its own unique storage keys.

Before accepting uploads, enable a native compute trigger on the bucket's owning
app (the app running `file-api.mjs`). Include every desired managed trigger:

```yaml
buckets:
  - bucket: your-bucket-name
    app: imports
    prefix: GREGALE_S3_IMPORTS
    triggers:
      - id: csv-upload
        prefix: imports/
        suffix: /input.csv
        events: ["s3:ObjectCreated:CompleteMultipartUpload"]
```

Historical completions are not delivered retroactively. Managed reconciliation
checks the observed revision and preserves manual rules; its list replaces the
app's managed rules. Existing storage preview, MFA and
plan gates apply. Notifications POST to `/`. The adapter requires gateway-owned
source/app/account headers, then re-reads native invocation proof and checks the
payload. Public body/header claims alone are refused. Set `HOST=0.0.0.0` for the
deployed process behind Gregale's gateway; other hosting needs an authenticated
proxy that strips forged platform claims. The backend token needs invocation
read, app read, storage read/write, Jobs, realtime publish and subscription-ticket
issuance access. Output reports do not match the trigger. Enable the existing
retained delivery and v2 resume preview gates and use an application-authorized
private endpoint. Ticket issuance retains the backend MFA requirement. Browser
users receive only a 30-second ticket scoped to their owned import channel.

Pause preserves the remote session. After a reload, enter the same application's
token and choose **Resume saved import**. Select the same CSV when its upload is
unfinished; a confirmed upload or completed import reopens without selecting a
file. The browser saves
only the file identity and credential-free checkpoint in localStorage. **Discard
upload** explicitly aborts it. A completed upload resumes without another PUT.
Every file/session/sign/complete/abort/start/progress/snapshot/ticket/report route checks the
durable record's owner first, so knowing a file ID does not grant another user
access. The backend exposes only the operations for that record, with fixed
keys, type and size; it does not proxy arbitrary storage requests.

`file-live.mjs` shares one bounded native Jobs observer per import within a
server process. It renews a 35-second viewer lease after issuing a ticket,
observes at 500 ms while active, and stops after verified completion or lease
expiry. This moves progress polling to the backend; native Jobs does not yet
push its aggregate status. Notifications only invalidate authenticated
snapshots, so duplicate messages and reconnects cannot claim completion.
Report-ready additionally verifies results and the stored rejection CSV before
enabling downloads. On process restart, the existing durable receipt restores
the run. Observer leases and messages carry no business side-effect guarantee.

Changing the demo token clears the old live view and download access immediately.
In a product, also revoke the old principal's native subscription tickets when
logging out or revoking membership; changing an app token alone does not revoke
an already-issued ticket. Keep tickets in memory and return them with no-store.
See [live job SDK usage](../../docs/sdk-live-jobs.md) for Node/Python async iterators,
the React hook, custom transports and session lifecycle requirements.

Before submitting rows the backend reads the stored bytes, checks the initially
declared size and fixed-block source digest, and uses an immutable version when
available. Submission intent is saved before Jobs access. If acceptance is
uncertain, its ledger remains `unresolved` and duplicate deliveries never POST
again. Inspect that intent/run explicitly rather than creating another
submission. Row/task failures remain visible in progress and results; the CSV
report lists validation rejections. Stored reports and inputs remain after task
cancellation, following the application's retention policy.

The local command above now also checks browser uploads, application restarts,
cross-language upload checkpoints, managed result storage with actual Node and
Python workers, and Bob being denied every operation on Alice's files. When
Chrome is available, it runs the real browser flow including private realtime
progress, download, reload/resume without the CSV, and a switch to Bob. The
agent flow exercises ticket expiry/reconnection. Set `GREGALE_LOCAL_CHROME` to a
Chrome/Chromium executable on other systems. Storage, scheduling and realtime
are local protocol fixtures; this does not establish deployed preview access,
provider behavior or bucket CORS.

`file_pipeline.py DELIVERY.json` consumes the same event/ledger using Python.
Run it on a trusted host/adapter with the environment above and
`GREGALE_FILE_APP_ID` (the owning app UUID). Its private input JSON contains
`invocationId` and the original JSON `body` string. It independently verifies
native proof and ownership. Local tests run competing Node/Python consumers,
re-delivery through the other SDK, and delivery after app restart with the
uploader gone; each upload creates one retained submission.

Recover a known accepted run by placing its ledger `identity` and inspected
`run_id` in a private JSON file:

```sh
python examples/sdk-csv-jobs/file_pipeline.py recovery.json reconcile
```

Reconciliation reads Jobs and updates the ledger only after matching the exact
saved manifest/account/endpoint/contract. It never resubmits. Retain ledger rows
while old notifications can replay. The demo's private upload records need a
shared state directory; replicated products should move ownership records into
their application database too.

## Durable three-stage pipeline (preview)

The optional pipeline mode composes three native Jobs under the existing
code-first workflow runtime: **validate CSV → import rows → write rejection
report**. `pipeline.mjs` and `pipeline_driver.py` define the same typed stages.
Their generated contracts cover the source file, row inputs, and final report.
Existing upload/file-triggered routes retain their behavior.
Use a separate `pipelines/` source prefix for this mode so the existing
`imports/` storage trigger does not also launch the single-job path.

Initialize the application receipt table explicitly:

```sh
psql "$DATABASE_URL" -f sdk/request-contracts/job-pipelines.sql
```

Configure native Job templates named `csv-validate`, `csv-import`, and
`csv-report`. Each task image runs `pipeline-worker.mjs` or `pipeline-worker.py`
with `GREGALE_CSV_PIPELINE_STAGE=validate|import|report`, the matching JSON
contract, database access for import, and the managed-file environment above.
These workers verify file hashes, preserve stable row identities and write the
report under a fixed import key. Report retries first check for exact existing
bytes before attempting a create-only write.

`pipeline-api.mjs` serves the protected workflow entry. Configure the owning
app's code workflow with `code: {path: /csv-pipeline, timeout: 30s}` and a
`csv-pipeline` workflow name. Set a scope bound to this app/account:

```sh
export GREGALE_PIPELINE_SCOPE='{"base_url":"https://your-api","account_id":"ACCOUNT_UUID","app_id":"APP_UUID","app_slug":"imports"}'
node examples/sdk-csv-jobs/pipeline-api.mjs
```

The backend token needs Jobs, workflow, account/app read and managed-file access.
Expose the process through Gregale's trusted gateway using `HOST=0.0.0.0` in a
deployed image. `serveWorkflows` retains its native source validation. Do not
expose the local protocol drivers as public endpoints.

After authenticating the application user, look up their owned uploaded file
and start using `createJobPipelineClient`/`JobPipelineClient`. The input is
`{import_id, source}` where source is a credential-free artifact descriptor:
`{version:1, scope:{base_url,app_slug,bucket}, owner_id, key, size_bytes, sha256}`.
Include `version_id` when available. SHA-256 here hashes the **whole file**;
the resumable-upload checkpoint's fixed-block `source_digest` is different.
Compute/retain this artifact descriptor on the trusted application backend.
Never accept a caller-selected key or owner without checking its owned record.

Save the returned pipeline checkpoint or workflow run ID. Either SDK can attach
and inspect the same run after the agent exits. `pipeline.inspectJob` /
`pipeline.inspect_job` supplies stage receipts and confirmed Job progress.
`jobPipelineLiveSnapshot` / `job_pipeline_live_snapshot` combines these with
native workflow status for the existing private live-view/React transport; a
report-ready callback reads and verifies the report before enabling download.
Final output contains `{report, saved, rejected}`. Download the report through
an authenticated app route using `readPipelineArtifact` / `read_pipeline_artifact`.

Start and cancellation have bounded request deadlines. Native cancellation
stops orchestration; already accepted Jobs continue until separately cancelled.
Failed native code workflows cannot currently be continued. Attach-by-ID is
read-only recovery; starting a new run is an explicit choice with new step
identities. Retain receipt rows for the workflow's possible replay lifetime.
The default 64 KiB recorded-value budget can be reached before the CSV's file or
1,000-row cap; use small demonstration files or artifact-shaped stage results.
See [typed pipeline usage and recovery](../../docs/sdk-job-pipelines.md).

Run the local pipeline journey with the same SDK/dependency setup as above:

```sh
node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

It uses real PostgreSQL and alternating Node/Python drivers and task workers,
with durable journals/receipts, dropped journal responses, owned source/report
files, live readiness, account/user isolation and cancellation. Workflow timers,
scheduling and storage are local protocol fixtures; deployed acceptance remains
separate. Regenerate each `*-generated.ts` / `*_generated.py` with the existing
job type generator when changing the corresponding `*-contract.json`.

## Reviewed import (preview)

Set `GREGALE_REVIEWED_PIPELINE=1` to use the separate `csv-reviewed-pipeline`
contract. It runs validation, pauses to review the exact source descriptor and
validated row inputs, and imports/reports only after approval. Rejection,
expiry, and cancellation return a typed business outcome with no import writes.
The original `csv-pipeline` and file-upload examples keep their existing behavior.
`reviewed_pipeline.py` and `pipeline.mjs` define the same typed workflow.
Generate review/root types with the existing `job-types` commands, using
`review-contract.json` and `reviewed-pipeline-contract.json` respectively.

Apply the existing job/pipeline schemas and
[`sdk/request-contracts/pipeline-gates.sql`](../../sdk/request-contracts/pipeline-gates.sql)
as explicit application migrations. The API refuses to start without the table.
Use the same primary and acknowledged autocommit connections for every adapter.

Configure `GREGALE_REVIEW_USERS` with demo application identities, for example:

```json
{"alice":{"token":"YOUR_ALICE_APP_TOKEN","owner_id":"alice","roles":["reviewer"]},"bob":{"token":"YOUR_BOB_APP_TOKEN","owner_id":"bob","roles":["reviewer"]}}
```

These are application tokens. Keep `FAAS_TOKEN` on the backend. Replace this
static demo identity map with your real session/membership lookup in production;
identity, owner and roles must never come from the decision request body.
Set `GREGALE_REVIEW_REALTIME` to your private managed endpoint configuration:
`{"url":"wss://YOUR_ENDPOINT","endpointId":"YOUR_ENDPOINT_ID"}`.
Start `pipeline-api.mjs` with the same database, bucket, scope and backend token
as the existing pipeline example. Serve the native workflow entry at
`/csv-reviewed-pipeline`; it retains the SDK's gateway source checks.

Start the reviewed pipeline with the existing authenticated application SDK
flow, then visit `/?run=WORKFLOW_RUN_ID` on the API server. Enter Alice's app
token, attach, inspect source digest/rows/expiry, and approve or reject with a
note. Bob cannot attach to Alice's run. A successful import enables the verified
report download. Changing credentials clears the review and report immediately.
The browser uses private realtime tickets and the existing live snapshot bridge.
Its tokens and reviewed rows are not saved to local storage.

The server runs a bounded pending-delivery recovery pass every second, including
when no browser is attached. The ledger survives server/driver restarts. Open
and resolution steps are native journal steps; decision events only wake them.
An uncertain event acknowledgment preserves the decision for recovery using the
same native key. Keep root run checkpoints and gate rows for your audit period.
Reviews/replies are bounded at 16 KiB, so this demo is for small CSV imports;
large imports should review a verified immutable plan artifact and its digest.

Run the local acceptance fixtures (PostgreSQL and Chrome paths use the same
`GREGALE_LOCAL_*` settings as the original example):

```sh
npm --prefix sdk/node run build
node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

They exercise both original/reviewed modes, alternating Node/Python drivers and
workers, dropped journals, a lost event ACK, two-user isolation, browser approval
and report download, concurrent opposing Node/Python decisions, database expiry
and parent cancellation. See [SDK gate semantics](../../docs/sdk-pipeline-gates.md)
for backend authorization, recovery and native workflow limitations. The native
control plane and realtime transport in this test are local protocol fixtures.

## Batch imports (preview)

`pipeline-batch.mjs` and `batch_pipeline.py` reuse `csv-reviewed-pipeline` for
multiple CSV files. The generated `batch-contract.json` input is
`{imports: [{id, value: {import_id, source}}]}` with stable item IDs and the same
owned source descriptors as a single import. Generate TypeScript/Python types
with the existing `job-types` commands. Each child validates its file, waits for
its own review, imports rows and produces a verified rejection report. The parent
returns ordered child outcomes and a combined saved/rejected row summary.

Install the existing Job/gate/receipt schemas plus:

```sh
psql "$DATABASE_URL" -f sdk/request-contracts/pipeline-children.sql
```

Set `GREGALE_BATCH_PIPELINE=1` alongside the scope, database, bucket, backend
credentials, application users and private realtime endpoint from the reviewed
example. Start `pipeline-api.mjs`. Publish two native code workflow definitions:
`csv-batch-pipeline` with `code.path: /csv-batch-pipeline` and
`csv-reviewed-pipeline` with `code.path: /csv-reviewed-pipeline`. Both definitions
are served with the SDK's native gateway source checks. The local CLI drivers
remain test-only and must not be exposed as HTTP routes.

After application authentication, start the batch using a typed pipeline client
bound to the batch contract and current owner. Retain its root run ID/checkpoint.
Visit `/batch-review.html?run=PARENT_RUN_ID`, enter your application token, and
attach to the private live parent view. Each child links to its existing review
and report screen. The parent view displays pending reviews, per-child states,
completed results and totals. Changing credentials clears the displayed state.
The protected backend `/batches/ID/view`, `/snapshot` and `/ticket` routes verify
native parent ownership before exposing child metadata or issuing tickets.
No platform token is sent to the browser.

The example uses concurrency two, a three-minute batch deadline, collected
outcomes, and cancellation of child orchestration on deadline/parent stop.
It runs bounded child recovery every second, alongside gate delivery recovery.
Accepted Jobs retain their own cancellation semantics. An unknown child-start
reply stays unresolved until a trusted operator identifies the exact run and
calls `coordinator.reconcile`; it is never automatically re-posted. See
[policy, lineage, budgets and recovery details](../../docs/sdk-pipeline-composition.md).

```sh
npm --prefix sdk/node run build
node --test examples/sdk-csv-jobs/composition-local.test.mjs
```

This fixture uses a real isolated PostgreSQL cluster, alternating Node/Python
parent/child drivers and actual workers, two competing CAS admission drivers,
a lost child-start ACK, dropped journals, verified-ID adoption, current reviewer
auth, two-user isolation, native-status-based child approval views and Chrome
parent progress. It confirms two child workflows, six Jobs and two verified
reports. The native control plane and realtime transport are local protocol
fixtures. Set `GREGALE_LOCAL_PYTHON`, `GREGALE_LOCAL_PG_BIN` and
`GREGALE_LOCAL_CHROME` as for the other local examples when paths differ.

## Event-triggered reviewed imports

`import.requested` can now start the same reviewed pipeline. The event's generated
payload contains only `import_id`. Application-owned immutable records supply its
authorized owner and verified source file; a producer cannot claim another owner
or make the consumer read an arbitrary file by changing an event payload.

The full flow is atomic business record + publication outbox -> authenticated CloudEvent -> shared durable
start receipt -> validation Job -> private browser approval -> import Job ->
verified report. [pipeline-events.mjs](pipeline-events.mjs) and
[event_pipeline.py](event_pipeline.py) implement the same adapter using
[event-contract.json](event-contract.json). They require application-supplied
delivery verification, current owner authorization and immutable-record lookup.
Reuse the reviewed pipeline's schema, workers and private realtime setup.

Generate payload types after changing the event contract:

```sh
node sdk/node/dist/event-types.js examples/sdk-csv-jobs/event-contract.json typescript examples/sdk-csv-jobs/event-generated.ts
python -m faas_sdk.preview.event_types examples/sdk-csv-jobs/event-contract.json python examples/sdk-csv-jobs/event_generated.py
```

Install [the start-receipt schema](../../sdk/request-contracts/event-pipelines.sql)
as the application owner. Both adapters use the same primary database and
acknowledged autocommit statements. The local example stores immutable business
inputs in `sdk_csv_event_inputs(import_id uuid PRIMARY KEY, owner_id text,
input jsonb)`; `input` is the reviewed pipeline input with its verified source.
Keep those records private and insert-only. [pipeline-producer.mjs](pipeline-producer.mjs)
and [event_producer.py](event_producer.py) atomically insert the authorized import
and stage its generated event. Apply `eventOutboxSchema` / `EVENT_OUTBOX_SCHEMA`
explicitly in the application migration transaction too, including the upgrade
for existing Go outboxes. A separately scheduled bounded outbox worker publishes
after acknowledged claim commit. See [typed publication and migration](../../docs/sdk-event-outbox.md).
The pipeline adapter coordinates consumption and workflow starts.


Configure the selected environment's native event subscription to invoke your
authenticated backend. Its verifier must check the delivery boundary before
using `eventConsumerRequestFromHeaders` / `event_consumer_request_from_headers`.
Context headers alone are not authentication. The local fixture uses a separate
private ingress credential; `event_driver.py` and the workflow journal drivers
are fixture programs and must never be exposed as HTTP handlers. Integrate the
factories into an existing backend using its current principals and role checks.

```sh
npm --prefix sdk/node run build
GREGALE_CSV_EVENT_PIPELINE=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

This first commits the import and typed publication intent atomically against
real isolated UTF-8 PostgreSQL and proves a failed transaction leaves neither.
Node publishes, the native fixture accepts the event but drops the response,
and Python reconciles the retained receipt without republishing. Confirmed
settlement erases the saved payload. The accepted event supplies the authenticated
consumer delivery, with an acceptance ID above JavaScript's safe integer limit.

Competing Node/Python consumers then handle that delivery. The native fixture
accepts the workflow but drops the start response.
Both consumers report `start_unknown`; Python reconciles the externally inspected
run ID and Node observes the same receipt. Repeated deliveries never start another
run. The recovered run then completes the existing mixed-language workers,
two-user access checks, Chrome review, live progress and verified CSV report.
Native event delivery, control-plane and realtime transports are local protocol
fixtures; this is not deployed scheduler/broker acceptance.

Retain start receipts for the entire event replay window. Unknown acceptance
requires authorized inspection/reconciliation, including after native key
retention; changing the key, adapter name or subscription is not recovery. See
[SDK event pipelines](../../docs/sdk-event-pipelines.md) for deadlines,
cancellation, retention and compatibility details.

## Scheduled reviewed imports

The reviewed CSV pipeline can run nightly from a native workflow schedule.
[pipeline-schedule.mjs](pipeline-schedule.mjs) and
[scheduled_pipeline.py](scheduled_pipeline.py) bind its generated input contract,
authenticated owner, verified file snapshot and scope to a 02:00 UTC calendar.
Set the returned `trigger` on the deployed `csv-reviewed-pipeline` workflow,
using the existing handler, `steps: []` and
`code: {path: /csv-reviewed-pipeline, timeout: 30s}`. Deploy through the normal
reviewed path, then pin the schedule client to the resulting deployment ID.

This uses fixed input: the same immutable import ID/source is reviewed on each
occurrence and business writes retain their idempotency. For newly arriving
files each day, define a scheduled coordinator that resolves current authorized
imports and starts typed children. It must not mutate the captured snapshot.
Only the live default deployment fires; Node/Python handlers need support for
`trigger.kind: schedule` before enabling the trigger.

```sh
npm --prefix sdk/node run build
GREGALE_CSV_SCHEDULED_PIPELINE=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The test uses real isolated UTF-8 PostgreSQL. Two competing native-admission
fixture transactions create exactly one occurrence and one workflow run. Neither
SDK submits a manual workflow start. Node and Python produce the same schedule
identity, preview and checkpoint, and reject the other application user. The run
then completes mixed-language Jobs, private Chrome approval, live progress and
verified report download. [schedule_driver.py](schedule_driver.py) is a private
fixture program, not an HTTP handler. Calendar/admission, control-plane and
realtime fixtures do not establish deployed scheduler acceptance.

See [the SDK guide](../../docs/sdk-pipeline-schedules.md) for DST/catch-up preview,
scoped history, explicit replay, unknown replies, deadlines and cancellation.

## Stripe-paid reviewed imports

A verified `payment_intent.succeeded` can stage the existing `import.requested`
event, then run validation, private review, import and verified reporting.
[paid-producer.mjs](paid-producer.mjs) and [paid_producer.py](paid_producer.py)
lock an application payment row, derive its current owner/customer and verify
integer-cent price/currency. Provider `metadata.owner_id` grants no access.
The immutable uploaded source comes from trusted business data.

Apply [paid-schema.sql](paid-schema.sql), the exported Stripe handoff schema,
and the event pipeline/outbox migrations as the application migration role.
Use the same primary database for business and SDK tables. Configure a native
Stripe app-delivery endpoint at `/webhooks/stripe`, pin its API version and mode,
and authenticate the platform boundary before reading its protected headers.
The example's private test pipe and fixture gateway credential demonstrate that
boundary; they are not public HTTP handlers or production authentication.
See [adapter setup and recovery](../../docs/sdk-stripe-webhooks.md).

From the repository root, after building the Node SDK and installing the Python
SDK with `psycopg[binary]`, run:

```sh
GREGALE_CSV_STRIPE_PIPELINE=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

This adds real PostgreSQL receipt and payment tables to the existing acceptance
flow. Native ingress fixtures validate Stripe-style raw-byte HMAC signatures
and overlapping rotation keys. A dropped ingress reply is retried against the
same native UUIDv5 receipt. Concurrent Node/Python adapters commit one business
update and one outbox intent; different provider IDs and an older failure cannot
repeat the payment effect. Forged delivery, another user and revoked access fail.
The test then recovers lost publication/start replies and completes the full
mixed-worker reviewed report flow with Chrome. Native APIs, ingress and event
transport are fixtures; a deployed Stripe/broker integration is not exercised.

## Agent tools for reviewed imports

[pipeline-tool.mjs](pipeline-tool.mjs) and [pipeline_tool.py](pipeline_tool.py)
expose the existing reviewed pipeline as five generated MCP tools: start,
status, result, cancel and reconcile. The Python definition preserves the
[generated input/output types](reviewed_pipeline_generated.py). Each stable
request ID names an immutable authorized import record; supplied input must
match that record. Credentials and model-supplied owner fields cannot grant
access. Configure both the receipt store and existing human gate store.

Install the exported `pipelineToolSchema` / `PIPELINE_TOOL_SCHEMA` with the
application migration role on the same primary used by both SDKs. The local
fixture adds `sdk_csv_tool_imports(request_id,owner_id,enabled,input)` for current
access and immutable input lookup. In production, authenticate the principal in
your MCP host and register `adapter.tools` / `adapter.call` with its existing
transport; the private Python pipe is only a local test driver.

After building Node and installing Python with `psycopg[binary]`, run from the
repository root:

```sh
GREGALE_CSV_TOOL_PIPELINE=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

An authenticated JSON-RPC fixture exercises initialization, tool discovery and
calls. Concurrent Node/Python starts share one PostgreSQL receipt despite a lost
native acceptance reply. Python reconciles the exact retained run; a new Node
call reuses it. Another user and revoked access are rejected. Both SDKs then
report `awaiting_approval` and the private human-review link without deciding
the gate. Chrome approves the exact source and rows; mixed workers complete the
import and both tools retrieve the same validated report output.

This uses real PostgreSQL and Chrome with native API and MCP transport fixtures.
It is not a deployed MCP publication or model-provider integration. See
[the complete tool contract](../../docs/sdk-pipeline-tools.md), especially receipt
retention, total deadlines, explicit cancellation and unresolved-start recovery.

## Reviewed cleanup after report failure

[compensation.mjs](compensation.mjs) and [compensation.py](compensation.py) define
the same `csv_cleanup` recovery. The original pipeline validates and imports
rows, then its report stage fails. Recovery verifies those source receipts and
builds immutable cleanup commands in reverse import order. Expected fingerprints
come from the original validated rows, so a later user edit cannot become an
approved deletion merely because planning observes it.

Install [pipeline-compensations.sql](../../sdk/request-contracts/pipeline-compensations.sql)
alongside the existing Job, pipeline, gate and tool schemas. The local fixture
adds `sdk_csv_recovery_owners` for current ownership/access and
`sdk_csv_recovery_audit` for committed deletes. Register the derived worker
contract from [compensation-contract.json](compensation-contract.json), then run
[compensation-worker.mjs](compensation-worker.mjs) or
[compensation-worker.py](compensation-worker.py) as its task entry. Publish the
returned definition as the native `recover-csv-cleanup` code workflow. Its drivers
retain native workflow gateway checks; backend tokens stay in the application.

After building Node and installing Python with `psycopg[binary]`, run from the
repository root using the same PostgreSQL/Chrome dependencies as the earlier
journeys:

```sh
GREGALE_CSV_COMPENSATION=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The fixture injects report failure after successful imports. Concurrent Node and
Python planning returns one shared plan; another user and revoked access fail.
A lost recovery start reply is reconciled to the exact run in Python. Chrome
approves the plan through the authenticated human gate. The fixture then edits
the first row before cleanup. The unchanged second row is deleted once, including
a dropped task acknowledgement and a retry in the other language. The edited row
survives, recovery returns `needs_review`, and both SDKs read identical effect
receipts. `python-fixture.mjs` is a private persistent local test driver, not an
MCP or production worker transport.

This journey uses real PostgreSQL and Chrome with native control-plane and MCP
fixtures. It does not qualify deployed publication or external-service rollback.
See [the compensation guide](../../docs/sdk-pipeline-compensations.md) for source
stage declarations, SQL-only callback rules, retained review stops, revisions
and explicit new-attempt recovery.

## Approved refunds after paid import failure

[refund.mjs](refund.mjs) and [refund_backend.py](refund_backend.py) define the same
typed Stripe refund action. The backend creates a private refund request only
after checking the correct owned cleanup's completed result and retaining its
checkpoint. Each operation rechecks the request, payment owner/current access,
exact amount/currency and verified native cleanup completion.

Install [outbound-actions.sql](../../sdk/request-contracts/outbound-actions.sql),
the existing gate/tool schemas, and [refund-schema.sql](refund-schema.sql) after
the paid CSV schema. Keep refund requests private and bind each business UUID to
one immutable application decision. The SDK stores the action intent separately
from its workflow attempts and external observation.

Configure a Stripe outbound integration bound to the app. Allow `GET /v1/account`,
`GET /v1/refunds` and `POST /v1/refunds`; its credential stays in Gregale's sealed
integration configuration. Set `GREGALE_STRIPE_INTEGRATION`,
`GREGALE_STRIPE_ACCOUNT`, `GREGALE_STRIPE_API_VERSION` and the authenticated human
review base URL `GREGALE_REFUND_REVIEW_URL`. The examples have fixture defaults for
these identifiers/version; supply your application's actual configuration when
deploying. Gregale provides `GREGALE_OUTBOUND_URL` inside the bound app.

[refund-api.mjs](refund-api.mjs) serves the gateway-checked native workflow entry
at `/action-csv-refund`; publish the action definition as the corresponding code
workflow. Register `createOutboundActionMCP(action)` / `OutboundActionMCP(action)`
with the application's authenticated MCP host. Configure the human gate service
with the returned root/review contracts and current reviewer authorization.
Application authentication/review routes belong in that backend; the private
fixture drivers are not production MCP or authentication transports.

After the Node build and Python dependency setup used by the earlier journeys,
run from the repository root:

```sh
GREGALE_CSV_REFUND=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The journey verifies a signed payment event and imports the paid CSV. Its report
fails; a human approves SQL cleanup and both unchanged rows are removed. A second
human gate then displays the exact payment, amount, currency and refund binding.
Concurrent Node/Python preparation shares one PostgreSQL intent. Another user
and revoked access are rejected, and a lost workflow start reply is reconciled
to the exact native run.

The simulated Stripe provider accepts one refund and drops the response. The
workflow retains an unknown result; Python reconciles the provider record by
GET and Node reads the same confirmed action observation. The POST count stays
one. The original edited-row compensation journey remains available separately.
This uses actual PostgreSQL and Chrome with native and Stripe protocol fixtures;
it does not issue a live refund or qualify a deployed integration. See
[the action guide](../../docs/sdk-outbound-actions.md) for retention, cancellation,
provider limits, verified webhook wakeups and unknown-outcome handling.

## Query context before recovery

The paid refund journey now lets applications and agents inspect the data needed
to choose recovery: search owned imported rows, inspect the failed report stage,
read its failure-summary resource, and read current payment amount/currency/state.
The existing cleanup and refund still require their separate human approvals.

[query-contracts.json](query-contracts.json) supplies shared input/row contracts;
[query.mjs](query.mjs) and [query_backend.py](query_backend.py) implement fixed
read-only SQL handlers. Every query enforces the current owner in SQL, with
authorization before/after the read. Each statement runs in a read-only
transaction with a five-second statement timeout. The report loader maps a
private import ID to a backend-materialized failure summary and verifies SHA-256.
It does not retrieve arbitrary paths or URLs.

Apply [query-schema.sql](query-schema.sql) with the application migration role.
The backend creates context records only after verifying the original failed
pipeline (the acceptance does this after compensation planning). Use a restricted
read-only serving role in your application. Set `GREGALE_QUERY_CURSOR_KEY` to a
secure backend-only 32–128 character printable ASCII secret shared by Node/Python.
The local acceptance generates a fresh fixture key before starting its workers.

```js
const {tools, report, adapters} = await csvQueries({sdk, pool, scope, cursorKey});
const page = await tools.rows.page({import_id: importId, search: '@example.test'},
  authenticatedPrincipal, {timeoutMs: 5000, pageSize: 20});
const payment = await tools.payment.page({import_id: importId},
  authenticatedPrincipal, {timeoutMs: 5000});
```

Register the adapters with your existing authenticated MCP host. The failure
query returns a resource link and template; reading the link reauthorizes the
current principal. Generated tool arguments cannot supply ownership claims or SQL.
The private Python fixture pipe is only a test transport, not a public auth route.

Run the full flow with the existing paid refund acceptance command above
(`GREGALE_CSV_REFUND=1`). It verifies identical Node/Python descriptors, pages and
resource bodies, a Node cursor continued by Python, concurrent authorized users,
Bob's denial for Alice's data, revocation, digest tampering, and Chrome reads
before cleanup/refund approval. See [SDK query tools](../../docs/sdk-query-tools.md)
for cursor retention, limits and host registration.

## Deploy the CSV MCP toolkit

The Node and Python definitions mount the CSV query and report adapters together
with the cleanup and refund workflows. The shared
[scope configuration](application-toolkit.json) defines exactly which operations
an agent can discover and call. OAuth permissions and current ownership are
checked for each request. The existing browser reviews authorize each effect.

Use [application-toolkit.mjs](application-toolkit.mjs) or
[application_toolkit.py](application_toolkit.py) to construct the registration.
Set up the paid CSV schemas, provider integration, review endpoints, and query
cursor key described above. Apply the same definitions and policy on each
serving replica. See the [application toolkit guide](../../docs/sdk-application-toolkits.md).

To run both authenticated HTTP starters in the paid CSV acceptance, build the
Node SDK and install the local starter dependencies first. Install the
Python starter requirements into the same environment as the SDK, or point
`GREGALE_MCP_STARTER_PYTHONPATH` to a directory containing those dependencies.

```sh
npm ci --prefix cmd/gregale/templates/mcp-node-tasks
GREGALE_CSV_APP_TOOLKIT=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The test signs short-lived tokens with a fixture key, reads owned rows and
reports, rejects direct operations outside the caller's scopes, then starts
cleanup on Node and reconciles and reads the same receipt on Python. It also
prepares the same refund through both hosts, passes the separate human review,
and confirms the provider result after a lost response.

## Resume from the work inbox

The CSV toolkit mounts `work_inbox` alongside the existing cleanup and refund
operations. Install the [inbox indexes](../../sdk/request-contracts/work-inbox.sql)
with the receipt and application schemas. The inbox maps retained attempts to
the import using the application's owned records. Read permission requires both
import and payment read scopes for this combined inbox.

A new session can list work by `business_ref` (the import ID). A cleanup entry
waiting for approval returns the existing request and review link. An unknown
refund entry returns the existing action ID for provider reconciliation.
The same `GREGALE_CSV_APP_TOOLKIT=1` acceptance above verifies discovery from
both hosts, another user's denial, revoked access, and one simulated provider
effect. See the [work inbox guide](../../docs/sdk-work-inbox.md).

## Watch a CSV import's live work inbox

Use the same local dependencies as the application toolkit acceptance.

```sh
GREGALE_CSV_LIVE_INBOX=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The application serves ticket and snapshot routes for an owned import. Each
request verifies the authenticated user and current import access. The ticket
binds that viewer to one private channel. Both the Node and Python clients watch
their own import, with the human approval still handled in a separate browser
session.

Alice sees the cleanup gate once after duplicate notifications and can follow
the review link. She then sees the uncertain refund; after explicit reconciliation,
the example drops the notification and reconnects both clients to recover the
succeeded action from current state. Disabling Alice's import access ends both
Alice watches while Bob can reconnect to his own import. The fixture verifies
a single provider effect. See the [live work inbox guide](../../docs/sdk-live-work-inbox.md).

## Resolve the next step from the inbox

The application toolkit also publishes `work_inbox_follow_up`. Send an inbox
entry's `kind`, `name`, `request_id`, and `business_ref`. The resolver reads the
current receipt and checks the scopes of the operation it would return. The
cleanup entry yields an authenticated human review link. An uncertain refund
yields the exact reconciliation call. After reconciliation, the original
entry yields an observation call, which a read-only user can invoke.

Both `GREGALE_CSV_APP_TOOLKIT=1` and `GREGALE_CSV_LIVE_INBOX=1` exercise the
feature through the Node and Python MCP hosts. The example checks revoked
access, a second user, operation scopes, and one simulated refund submission.
See the [follow-up guide](../../docs/sdk-work-inbox-follow-ups.md).

The CSV run also drops the native cleanup start response after acceptance.
Two fresh MCP clients start from the import ID, resolve the original accepted
run, and reconcile concurrently. The fixture verifies one accepted cleanup run
and rejects a run with identical business input from a different invocation.

## Resume CSV cleanup from an MCP Task

Run the same local dependencies as the application toolkit example.

```sh
GREGALE_CSV_PIPELINE_TASKS=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The Node host returns a Task handle when the client opts in. The Python host
uses the same PostgreSQL binding to read the Task with a fresh authenticated
request. It sees the uncertain start and a verified reconciliation plan. Both
hosts expose the pending human review and then the same typed cleanup result
after approval. The test checks another user, missing scopes, revoked
application access, and one accepted native start across repeated calls.
See the [pipeline Task guide](../../docs/sdk-pipeline-tasks.md).

## Clarify a CSV column from an MCP Task

```sh
GREGALE_CSV_CLARIFICATION=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The clarification pipeline pauses when both `contact` and `alternate_contact`
are candidate email columns. Its typed form lets the operator choose one.
The Node and Python MCP starters share the Task receipt, gate decision, and
typed reply contract. The test starts the Task in Node, reconnects through
Python, submits the same answer concurrently, and verifies that a single
durable wake event resumes the run. It also checks a conflicting answer,
an expired question, scope and access revocation, and a second user's
attempt to read or answer the form. A client without form support can open
the same question through the authenticated review endpoint.

The same command exercises `subscriptions/listen`. A client watches the Task
start in Node and receives the form over SSE. It disconnects, submits the
answer, and reconnects through Python to see the typed result. The test also
checks that unchanged snapshots stay quiet, revocation closes an active
Task-only stream, and another user receives no Task snapshots.

## Read the CSV mapping report over MCP

Run the clarification acceptance command above. The resolved email-column
choice starts a typed report Job that writes a CSV to managed storage. The
completed Task and the direct `clarify_csv_columns_result` tool both return a
`mapping_report` resource link. The report names the selected column.

The test reads that link through Node and Python, repeats the reads, and
checks that a caller with revoked access or another user's token receives no
file bytes. It also replaces the stored bytes with different content of the
same length, replaces the file with an oversized body, and checks that an
oversized declared file is rejected before download.

The same run reads the scoped manifest from both hosts and generates standalone
Node and Python client modules in a temporary consumer directory. The test
checks scope filtering and detects a changed input schema with `--check`.
The generated Node client saves a JSON checkpoint and closes its watch after
seeing the column question. A separate Python process uses its generated
module to resume the checkpoint, send one answer, and retrieve the verified
mapping report after completion. See the
[Task binding guide](../../docs/sdk-pipeline-tasks.md#discover-contracts-and-generate-bindings).

## Release a new CSV mapping contract with a Task in progress

The same clarification acceptance publishes a new host view after the first
Task asks its question. It marks `clarify_csv_columns` as retained and makes
`clarify_csv_columns_v2` the default for `family:csv_column_mapping`. The
second revision requires a typed `source_system` input and offers one email
column. Fresh generated
bindings start a v2 workflow while a Python process answers the original
v1 question and reads its original `mapping_report`.

The acceptance also retries the saved v1 request against the upgraded host
and gets the original Task ID. It checks that a fresh start on the retained
revision, an incorrect digest, a changed retry input, and a different user
cannot dispatch work. PostgreSQL records one native run for each revision.
See [publishing a new contract revision](../../docs/sdk-pipeline-tasks.md#publish-a-new-contract-revision).

## Preview the CSV mapping before starting

The v2 CSV mapping tool publishes a typed preview with the current source
digest, email columns, expected report, and clarification requirement. The
same clarification acceptance reads the preview, replaces the source CSV,
and checks that confirmation returns `preview_stale`. It refreshes the
preview and confirms a Node-issued token through the Python MCP host. A
separate Python consumer retries after a simulated lost response and receives
the original Task. The test verifies one native v2 run and denies previews
from a second user. See the [preview guide](../../docs/sdk-pipeline-tasks.md#preview-a-pipeline-start).

## Keep the reviewed CSV version through clarification

The v2 preview records the version from a managed multipart upload. The test
starts the Task, waits for the column question, and uploads a new version at
the same key. Answering the question produces a report with the email from the
reviewed version. Retrying the start after replacement returns the same Task.
Two further runs remove the pinned version or revoke the source grant while
the Task is paused. Both SDK clients receive a typed source failure, and
neither run writes a report.

## Recover a failed mapping Task from a replacement file

The clarification acceptance removes a pinned CSV version and verifies that
the original report was never written. It previews a replacement with the
failed Task's source digest and the new source summary. It exercises revoked
access, a stale replacement preview, and a second OAuth user. After an explicit
confirmation on Node, the fixture drops the response and retries from a
generated Python client. Both clients resolve the same replacement Task.
The replacement links to the failed Task and writes one report using the
file reviewed at recovery.

## Hand off a CSV clarification

The clarification acceptance assigns a pending question to a named reviewer.
The reviewer reconnects through the other SDK under a second OAuth client and
submits the answer. The original agent sees the same Task complete with its
mapping report. The test checks an unassigned user, expired and revoked grants,
revoked reviewer membership, a racing revocation, and duplicate replies.
The durable gate decision records the reviewer's identity.

## Watch a reviewer's assigned CSV clarifications

The [reviewer inbox](../../docs/sdk-pipeline-tasks.md#list-and-watch-assigned-clarifications)
shows assignments across two CSV imports. The acceptance test lists one item
per page and resumes a Node page cursor through the Python MCP host. A watch
drops work when the reviewer role is revoked, ignores duplicate notifications,
and reloads current assignments after reconnect. A second reviewer sees only
their own handoff. The first reviewer opens the remaining form and answers it
from Python, clearing it from the inbox and continuing the original Task.

## Approve a staged CSV import from the reviewer inbox

The [assigned approval flow](../../docs/sdk-pipeline-tasks.md#assign-an-approval-to-a-reviewer)
takes the reviewed import through the Node and Python MCP hosts. The reviewer
finds the approval in their inbox and opens the exact stored CSV version and
validated rows before deciding. A rejection completes the Task with zero
writes. An approval releases the rows and produces the verified report.

```sh
npm --prefix sdk/node run build
GREGALE_CSV_ASSIGNED_APPROVAL=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

The acceptance test uses local PostgreSQL and the Node and Python MCP starter
dependencies. It checks an expired grant, revoked reviewer membership, a key
from an earlier preview, competing decisions, and a cross-host retry that
returns the same receipt and records one workflow wake.
