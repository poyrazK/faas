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
