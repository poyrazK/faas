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
python -m pip install -e sdk/python -r examples/sdk-csv-jobs/requirements.txt
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

Local integration requires PostgreSQL binaries and the example dependencies:

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

`file-api.mjs` and `file-index.html` extend this example with a complete file
workflow: authenticated application record → direct resumable upload → confirmed
stored CSV → typed jobs → progress → stored rejection report download. Both
worker languages can now use the SDK's built-in managed JobStore.

Configure an existing app and bucket with storage preview access and read/write
grants. Set bucket CORS to allow your application origin, PUT, and the returned
Content-Type header, exposing ETag and Content-Length. Configure the existing
`csv-import` Jobs template with the same database and SDK, and these worker vars:

```sh
export FAAS_API=https://your-gregale-api
export FAAS_TOKEN=your-backend-token
export GREGALE_FILE_APP=imports
export GREGALE_FILE_BUCKET=your-bucket-uuid
# On workers, prefer a credential scoped to this app/bucket:
export GREGALE_FILE_TOKEN=your-worker-storage-token
export GREGALE_IMPORT_STATE=/private/path/file-imports
export GREGALE_FILE_USERS='{"alice":"replace-with-alice-app-token","bob":"replace-with-bob-app-token"}'
npm --prefix sdk/node run build
node examples/sdk-csv-jobs/file-api.mjs
```

Open `http://127.0.0.1:8080`, enter Alice's application token and choose a CSV.
The backend credential stays on the server. Replace the static token map with
your normal application authentication before using the example as a product.
The demo listens on loopback; production hosting also needs TLS. This file flow
accepts up to 1 MiB per CSV and generates its own unique storage keys.

Pause preserves the remote session. After a reload, enter the same application's
token, select the same file and choose **Resume saved import**. The browser saves
only the file identity and credential-free checkpoint in localStorage. **Discard
upload** explicitly aborts it. A completed upload resumes without another PUT.
Every file/session/sign/complete/abort/start/progress/report route checks the
durable record's owner first, so knowing a file ID does not grant another user
access. The backend exposes only the operations for that record, with fixed
keys, type and size; it does not proxy arbitrary storage requests.

Before submitting rows the backend reads the stored bytes, checks the initially
declared size and fixed-block source digest, and uses an immutable version when
available. Submission intent is saved before Jobs access. If acceptance is
uncertain, inspect that intent/run explicitly rather than creating another
submission. Row/task failures remain visible in progress and results; the CSV
report lists validation rejections. Stored reports and inputs remain after task
cancellation, following the application's retention policy.

The local command above now also checks browser uploads, application restarts,
cross-language upload checkpoints, managed result storage with actual Node and
Python workers, and Bob being denied every operation on Alice's files. When
Chrome is available, it runs the real browser flow including progress, download
and reload/resume. Set `GREGALE_LOCAL_CHROME` to a Chrome/Chromium executable on
other systems. Storage and the scheduler are local protocol fixtures; this does
not establish deployed preview access, provider behavior or bucket CORS.
