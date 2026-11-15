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
