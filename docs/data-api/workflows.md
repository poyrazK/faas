# Recovery and background workflows

## Resumable storage and upload receipts

For an existing `storage_objects` installation, generate an owner migration:

```sh
gregale data-api storage sql --upgrade --sessions > migrations/0014_upload_sessions.sql
```

For a fresh installation use `storage sql --sessions`. Grant the serving login
access to the new session table and internal invoker functions, then apply the
migration and refresh the Data API schema cache through your normal migration
workflow. For example, with serving login `data_api` and schema `api`:

```sql
GRANT SELECT,INSERT,UPDATE,DELETE ON api.gregale_storage_uploads TO data_api;
GRANT EXECUTE ON FUNCTION
  api.gregale_storage_start(uuid,text,text,bigint,text,text,text),
  api.gregale_storage_part(uuid,integer,integer,text,text),
  api.gregale_storage_prepare(uuid), api.gregale_storage_put(jsonb,text),
  api.gregale_storage_finish(uuid), api.gregale_storage_abort(uuid) TO data_api;
```

The session table and functions are internal, unavailable through public REST
or RPC calls. Your existing storage-object RLS policies still authorize uploads
before transfers and again at commit. Sessions bind to the verified claims,
including tenant context; refreshed expiry/issue timestamps do not change scope.
Other claim changes, including a changed token ID (`jti`), require a new session.

```ts
const files = storage.from('uploads')
const previous = await files.head('report.pdf')
const saved = await files.uploadResumable('report.pdf', file, {
  expectedVersion: previous.version!,
  onSessionId: id => saveUploadId(id),
  onProgress: ({ uploaded, total }) => showProgress(uploaded, total),
})
```

Omit `expectedVersion` for unconditional replacement, use `null` for create-only,
or pass the opaque version from `head()` or a prior upload. A conflicting version
returns `412 storage_version_conflict`. Legacy files without object keys have no
conditional version until replaced with a versioned upload.

Persist `onSessionId` before any request is sent. If creation loses its reply,
`UploadSessionStartError.sessionId` also exposes the ID. Call `getUpload(id)` to
reconcile it; retry creation with `{ id }` only when needed. `sessionId` resumes
an existing session, while `id` explicitly repeats the original idempotent
creation. Never generate a replacement ID for an uncertain start.

After a network error or `503 storage_commit_unknown`, read the saved session
with `getUpload(id)`. Its completed receipt is authoritative. Resume with the
same file and ID:

```ts
await files.uploadResumable('report.pdf', file, { sessionId: savedUploadId })
// Stop an unfinished session and clean up its unreferenced bytes:
await files.abortUpload(savedUploadId)
```

The client verifies acknowledged checksums against the supplied file before
resuming. It does not automatically retry. Sessions last 24 hours, allow 16
active uploads per authorization context, and use 5 MiB chunks within the
configured object-size cap (at most 100 MiB). Four transfers per instance bound
chunk buffering. Session composition uses additional bucket reads/writes.

Ordinary PUT uploads also reconcile lost metadata replies. If the result stays
unknown, bytes remain intact. Deletion commits metadata before best-effort byte
cleanup. Keep receipts long enough for clients to reconcile; owner cleanup must
verify references before deleting expired chunks, abandoned objects or replaced
versions. An inconclusive read is insufficient evidence for deleting bytes.

## Exact numeric decoding

Generate matching types and explicitly select the SDK codec:

```sh
gregale data-api types NAME --numeric-mode lossless --output database.types.ts
# Local development and CI:
gregale data-api dev --once --numeric-mode lossless --check-types
```

```ts
const data = createDataClient<Database>({
  url, accessToken: () => session.token, numericMode: 'lossless',
})
```

Every JSON number becomes its original decimal text: `9007199254740993`, long
decimals, exponents, array elements and numbers inside JSON columns. `liveRead`
inherits the query client's codec. Default clients continue to return numbers.
Use explicit conversion when arithmetic is appropriate. This decoding mode
does not retain a JSON token's number-versus-string distinction for writes; do
not blindly round-trip decoded JSON columns. Runtime input validators remain a
separate output and cannot be combined with lossless type generation.

## Background imports and snapshot exports

The Teams starter includes migration `0013_background_data_jobs.sql`, a browser
job panel and `workers/data-jobs.mjs`. Register a reviewed import first, then
choose **Continue registered import in background**, or queue a full-team
snapshot export. Refresh progress later, cancel remaining work, download a
saved snapshot CSV, or download a failed-batch error report. Exports include all
visible orders in the chosen team; the foreground filtered CSV action retains
its existing behavior.

Run the worker image as an ordinary scheduled Gregale Job:

```sh
# Build workers/Dockerfile.data-jobs with the Teams project as its context using
# your normal Gregale image/build workflow, then use the resulting image ref.
gregale job create team-data-worker --image IMAGE_REF --schedule '* * * * *' \
  --parallelism 1 --timeout 120 --retries 0 -- node /app/data-jobs.mjs
```

Configure `DATA_API_URL` and a private backend `DATA_API_SERVER_KEY` in the Job's
environment using your secret-management workflow. Never put this key in the
browser. An RPC-restricted worker key needs `claim_team_order_job`,
`advance_team_order_job`, `retry_team_order_job` and `fail_team_order_job`; the worker acts for the
recorded subject using the standard server-key subject header. Membership is
rechecked in every transaction, including after a lease waits or expires.

The browser preserves an uncertain enqueue ID for the same API, team and user.
Resolve that operation before changing context. Queue operations are idempotent
by caller operation ID. Import batches remain atomic at 20 rows and resume from
durable offsets. Already committed batches remain after cancellation. Invalid or
revoked work records a stable error code and affected batch row range. Network
and 5xx failures schedule a retry from the saved progress and let newer jobs
proceed. Apply `0014_job_retry_scheduling.sql`: exponential delays range from
2 to 300 seconds, with quarantine after eight attempts. The panel shows
scheduled retries and blocked work, offers its diagnostic report, and allows
an authorized owner/admin to resume from saved progress or cancel. A lost
retry reply still leaves a delayed, reclaimable lease; it never proves rollback.

Exports save one PostgreSQL statement snapshot, bounded to 10,000 rows / 10 MiB.
Later edits do not change the saved artifact. Results and error reports live in
the customer database under RLS; they are not automatically published as Job
object-store artifacts. Apply `0015_job_history_retention.sql` and use owner maintenance below to
retire completed history and artifacts. Compact retired IDs prevent expired
operations from being recreated (`410 job_history_retired`). The
example allows 16 active or quarantined jobs per actor and tenant; scheduled capacity and
normal Job billing apply.

## Policy and RPC behavior checks in CI

Capture an owner-only local baseline after required authorization checks:

```sh
gregale data-api dev --once --behavior-output behavior.before.json
gregale data-api dev --once --behavior-baseline behavior.before.json \
  --behavior-replays behavior-replays.json --behavior-output behavior.after.json
```

The baseline records object names and digests, never SQL definitions. It covers
policy expressions, function bodies and private helpers, grants, inherited role
settings, defaults, constraints, triggers, view options and extension versions.
It does not change the public serving fingerprint. Changed objects must map to
named required authorization scenarios in the current development project:

```json
{"version":1,"objects":{"policy:api.notes.own_notes":["notes-rls"],"function:*":["rpc-access"]}}
```

Exact object IDs, `kind:*`, and `*` are supported. Prefer exact mappings for relevant
allow/deny scenarios. Missing mappings or scenarios that did not pass in this
run fail the command. A digest is a change detector, not proof of authorization.
Review and commit a new baseline only after reviewing the diff and replay scope.

## Hooks while the API is parked

The built-in `gregale data-api hooks enable NAME --drain-every 5m` cron remains
available. The optional Job adapter below uses a backend key and exposes a
separate owner observation route; both retain the same outbox leases.

Install the optional outbox observation helper for an existing installation:

```sh
gregale data-api hooks sql --upgrade > migrations/0015_hook_observation.sql
```

Grant `EXECUTE ON FUNCTION api.gregale_change_status()` to the serving login.
Fresh `hooks sql` output includes the helper. Owner-only requests with an
unrestricted backend server key expose `GET /__gregale/hooks/status` and
`POST /__gregale/hooks/drain`. They report pending, failed and leased counts,
oldest pending time and lag in seconds. Browser credentials cannot call them.

Schedule `node /app/hooks-worker.mjs` as a Gregale Job using the updated Data API
runtime image, with the API URL and private backend key in the Job environment.
Each invocation wakes the API through its normal HTTP route and drains bounded
batches. The API publishes under its own workload identity; outbox leases,
ordering and publication deduplication stay intact. Jobs and API wakes incur
normal usage. A schedule is a delivery opportunity, not a latency guarantee.

## OpenAPI 3.1

Authenticated `GET /openapi-3.1.json` returns OpenAPI 3.1 with components,
standard nullable schemas and single/bulk request unions. It retains the schema
fingerprint and profile filtering. Internal relay/session tables and unapproved
RPCs remain hidden. `/openapi.json` continues to serve the Swagger 2 contract.


## Exact numeric round-trips

For editable exact data, generate `--numeric-mode exact` types and pass
`numericMode: 'exact'` to `createDataClient`. `ExactNumber` distinguishes numeric
literals from strings and keeps their original decimal text in `source`.
REST, RPC and `liveRead` share this mode. The default and string-based lossless
modes remain available; fingerprints describe the same serving schema.

```ts
import { createDataClient, ExactNumber, stringifyExactJSON } from '@gregale/data'
const db = createDataClient<Database>({ url, accessToken, numericMode: 'exact' })
const result = await db.schema('api').from('prices').select()
// Numeric JSON values remain numbers when sent back, including nested JSON.
await db.schema('api').from('prices').insert(result.data!)
const amount = new ExactNumber('12345678901234567890.001')
const document = stringifyExactJSON({ amount, label: '12345678901234567890.001' })
```

Use `stringifyExactJSON` for storage or independent transports, and
`parseExactJSON` to restore exact values. Ordinary `JSON.stringify` also works
where `JSON.rawJSON` is supported; otherwise it refuses an ExactNumber rather
than changing its type. Numeric coercion requires an explicit conversion and a
rounding policy. Runtime numeric input validators remain a separate output.
Plain CSV import and saved-request replay reject numeric values that ordinary
JavaScript JSON would round; use a text projection for exact replay captures.

## Owner maintenance

Install cleanup fences after storage and optional upload sessions:

```sh
gregale data-api storage sql --upgrade --maintenance > migrations/0016_storage_cleanup.sql
# For a fresh installation: storage sql --sessions --maintenance
```

Run the runtime's `data-maintenance.mjs` in a separate trusted owner environment
with `MIGRATION_DATABASE_URL`, `DATA_API_SCHEMAS`, and the existing bound-bucket
`GREGALE_S3_*` configuration. Never give the serving process owner credentials
or grant its login access to the private maintenance schemas. The same worker
can retire Teams job history without buckets after migration 0015.

```sh
node data-api-runtime/data-maintenance.mjs --dry-run
node data-api-runtime/data-maintenance.mjs --execute
```

Dry-run is the default and changes no rows or objects. Schedule execution using
your normal trusted Job workflow; run the preview when changing bindings or
retention. The exported `maintainData` API accepts a longer grace period and
retention, plus inventory/delete adapters for testing or compatible storage.

Inventory covers immutable `_gregale/objects/` keys, including orphaned PUTs,
replaced versions and abandoned parts. Legacy name-based keys are left alone.
Objects must be at least five minutes old, beyond the two-minute transfer
bound. Metadata and active upload-session references are checked with owner
visibility and `row_security=off`; inadequate visibility fails the run.
Per-key SQL locks and durable deletion fences prevent concurrent reference
creation. Deletes use the observed ETag, retry uncertain outcomes with backoff,
and retain receipts. A changed object must age through the grace period again.

Each run reads at most ten inventory pages (10,000 keys), saves its continuation,
and handles at most 100 deletion candidates per schema. One database lease
prevents overlapping maintenance runs. Expired upload-session history and
completed/failed/cancelled job history have a default retention of 30 days;
blocked jobs require review. Full exports disappear with retired jobs. Compact
job IDs remain to prevent replay; storage delete receipts expire after the
retention period, beyond every supported transfer/session lifetime. Use fresh
upload IDs for new operations. Retention can be increased to 3,650 days.
