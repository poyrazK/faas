# Gregale Data API client

`@gregale/data` wraps PostgREST's typed client for Gregale application Data APIs.
The management SDK and account keys remain separate from application access.
This package is not published to npm. From an authorized checkout, run
`npm ci && npm run build && npm pack`, then install the produced tarball in
your application. The repository's existing SDK license/publication policy applies.
For a paired CLI/SDK artifact and a clean starter installation, use the
[Data API bundle workflow](../../docs/data-api-packaging.md).

```ts
import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  accessToken: async () => auth.getAccessToken(),
}).schema('api')

const { data, error } = await db.from('notes').select('id,body').range(0, 19)
await db.from('notes').insert({subject: auth.user.id, body: 'Hello'})
```

Generate `Database` with `gregale data-api types my-data --output
src/database.types.ts`. Use `--check` in CI to fail when your file differs
from the database. Refresh the serving schema cache after migrations with
`gregale data-api refresh my-data` and verify restart completion.

### Public data without a session

When the owner enables [public reads or public RPCs](../../docs/data-api.md#public-reads),
create the client with `anonymous: true`. Requests then go out without an
`Authorization` header whenever there is no token:

```ts
const visitor = createDataClient<Database>({ url: 'https://my-data.gregale.dev', anonymous: true }).schema('api')
const { data: products } = await visitor.from('products').select('id,name').limit(20)
const { data: results } = await visitor.rpc('search_products', { q: 'lamp' }, { get: true })
```

Anonymous callers must call public RPCs with `{ get: true }`; a `POST` returns
`405 public_rpc_requires_get`. Arguments and results keep their generated
types.

One client can serve signed-in and signed-out visitors: with `anonymous: true`,
`accessToken` may return `null`, and the request is then anonymous. An empty
or malformed token still throws before any request is sent. Without
`anonymous: true`, a missing token always throws, so a broken session never
silently becomes anonymous access:

```ts
const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  anonymous: true,
  accessToken: async () => (await auth.getSession())?.accessToken ?? null,
}).schema('api')
```

Anonymous requests through `expectedSchemaFingerprint` reach the API on a
contract-pinned path, so the Data API does not mark them for the edge cache.

### Live reads

A live read is a Data API read whose result stays current: it arrives once,
then changes as rows are written. Row-level security decides each row, as it
does for REST reads. Set it up once with
[`gregale data-api live enable`](../../docs/data-api.md#live-reads), which
prints the WebSocket URL and the database ID:

```ts
import { LiveConnection, liveRead, liveSocket } from '@gregale/data/live'

const connection = new LiveConnection({ connect: liveSocket('wss://my-data.gregale.dev/__gregale/realtime/ENDPOINT_ID') })
const feed = connection.query({
  database: DATABASE_ID,
  token: () => session.accessToken,
  ...liveRead(db.from('messages').select('id,body,author(name)').eq('channel_id', 7).order('id', { ascending: false }).limit(50)),
})
feed.subscribe(() => render(feed.getSnapshot().rows))
```

`liveRead` takes the read's rows type from the generated contract, and rows
decode as the REST API returns them. Each read carries exactly one
credential:

- `token`, the signed-in user's token, called again before it expires;
- `anonymous: true`, for relations in the Data API's public-read allowlist;
- `serverKey` (with an optional `subject`) for trusted backends. Server keys
  are refused from browsers.

React components use the hook from `@gregale/data/live/react` (React 18 or
later):

```tsx
import { useLiveRead } from '@gregale/data/live/react'

const { rows, status } = useLiveRead(connection, { database: DATABASE_ID, anonymous: true, ...liveRead(visitor.from('products').select('id,name')) })
```

The connection reconnects with backoff and resumes each read, keeping the
last rows meanwhile. `status` is `connecting` (also while reconnecting),
`live`, `stale` (the result could not be kept current, for example after a
credential expired) or `closed`.

### Pin requests to the generated contract

Regenerated TypeScript files also export a runtime `schemaFingerprint` value.
Opt in to an exact serving-contract guard when a client must fail before data
access against a different deployment contract:

```ts
import { schemaFingerprint, type Database } from './database.types.js'

const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  accessToken: () => auth.getAccessToken(),
  expectedSchemaFingerprint: schemaFingerprint,
}).schema('api')
```

The SDK requires 64 lowercase hexadecimal characters and uses a guarded route
for every REST/RPC request. The gateway authenticates the token and rejects a
mismatch with HTTP 409 and `error.code === 'schema_contract_mismatch'` before
forwarding to PostgREST. HEAD responses have no error body; inspect their status.
An unavailable serving fingerprint returns 503. Older runtimes return 404;
the client never falls back to an unguarded request. Deploy a guard-capable
runtime before enabling the option. Ordinary clients retain their existing URLs.

Exact matching also rejects compatible additive changes. Update client types
and coordinate deployment using the existing sync/refresh workflow. The guard
compares the runtime's captured startup contract, not a live database inspection;
out-of-band DDL or engine-only cache reloads can make that observation stale.
It does not prove RLS, function-body or data compatibility. Guard failures do
not automatically replay writes or update the pinned fingerprint.

Owners can enable selected older client fingerprints using a serving-bound
`DATA_API_ACCEPTED_CONTRACTS` policy generated by
`gregale data-api diff --baseline old.json --current new.json --accept-contract accepted.json`.
The command requires zero structural breaking changes, including approved breaks.
Clients keep the same option; authentication and RLS still apply. See the
[rollout guide](../../docs/data-api.md#accept-older-clients-during-a-compatible-rollout)
for review, deployment and revocation.

Generated files also export `Tables<'notes'>`, `TablesInsert<'notes'>`,
`TablesUpdate<'notes'>`, and `Enums<'state'>`. `Tables` includes read-only views;
write helpers accept tables only and preserve generated-column restrictions.
An optional second parameter selects an exported schema, for example
`Tables<'items', 'other'>`. The default is `api` when available.

```ts
import type { TablesInsert } from './database.types.js'
import type { QueryData, QueryResult, QueryError } from '@gregale/data'

const input: TablesInsert<'notes'> = { subject: auth.user.id, body: 'Hello' }
const query = db.from('notes').select('id,comments(id,body)')
type Rows = QueryData<typeof query>
type Response = QueryResult<typeof query>
type Error = QueryError<typeof query>
```

`QueryData` removes the nullable error result and preserves nested projection
inference. `QueryResult` describes the full awaited response. `QueryError`
describes its non-null error type. The SDK also exports PostgREST single,
maybe-single and array response types.

For an existing application, install an approved bundle without copying starter
scripts:

```sh
gregale data-api install --bundle /approved/data-api-bundle.json --directory .
# Use --client-directory web when package.json is in a project subdirectory.
gregale data-api install --restore --from /approved --directory .
```

Commit the artifact pin, client package.json and lockfile together. Restore checks
both archive checksums and the committed npm integrity before installing locked
dependencies. This workflow uses approved artifacts; public npm publication
remains subject to the SDK publishing policy.

The token getter runs on each request. Requests omit browser cookies and refuse
redirects so access tokens stay at the configured API. The client preserves
PostgREST relation, insert, update, filter, projection and relationship inference.
Generated view relationships preserve readable, complete key projections,
including renamed columns and view chains in the same exposed schema. Aliased
embeds and FK hints use the normal PostgREST select syntax. Cardinality follows
the underlying key constraint; view column types remain nullable. Expressions
and partial keys have no inferred path, and ambiguous embeds can fail at runtime.
Approved same-schema computed relationships use generated `SetofOptions` for
source, target and cardinality. A `ROWS 1` function embeds an object or null,
while its direct RPC returns an array. Relation-set RPCs support typed nested
joins, projections and filters on their result relation. Upgrade the CLI/runtime
for normalized version 3 contracts; version 1/2 baselines remain readable.
See [the Data API guide](../../docs/data-api.md).

For JSON/JSONB paths, use explicit JSON aliases to preserve SQL nullability:

```ts
import { selectJson, jsonText, jsonValue } from '@gregale/data'

const orders = await selectJson(db.from('orders'), 'id', {
  status: jsonText('metadata', 'status'),
  city: jsonText('metadata', 'delivery', 'city'),
  details: jsonValue('metadata', 'details'),
  first: jsonText('metadata', 'items', 0, 'name'),
}).eq('metadata->>status', 'paid')
// id retains its generated type; status/city/first are string | null.
// details is Json, including null; no arbitrary keys are inferred.
```

The second argument is a literal normal select string, or `''` for JSON aliases
only. Keep JSON paths in the third argument: native JSON paths inside the base
selection retain the upstream SDK's inference, which can omit SQL nullability.
Alias names must be known to TypeScript and must not duplicate base fields.
Source columns are checked against the
generated row contract. Tables, views, write representations and relation-set
RPC builders work with the usual filters, ordering, `.single()`/`.maybeSingle()`
and error handling. Select again to replace the projection.

Paths accept nonempty string keys and safe integer array indexes, including
negative indexes. Numeric strings select object keys; numbers select array
positions. Names are quoted and URL-encoded by the SDK; quotes, backslashes and
control characters in names are rejected before sending. Missing paths, SQL
NULL and JSON null all yield null with `jsonText`. `jsonValue` preserves JSON
values, but HTTP JSON cannot distinguish extracted JSON null from SQL NULL.
Use database predicates when that distinction matters. See the guide for
JSONB `eq.null` versus SQL `is.null` and RLS examples.

Opted-in PostgreSQL functions appear in the generated `Functions` contract.
Call `db.rpc('function_name', { named_argument: value }).retry(false)` using an
application JWT. Gregale permits POST RPC only; omit `get` and `head` options.
Generated `CompositeTypes<'name'>` helpers describe named PostgreSQL composite
objects, including nested values and arrays. Approved RPCs use these types for
arguments and results; a composite result is an object and a SETOF result is an
array. Composite fields remain nullable, including domain attributes.
Arguments, defaults and results are inferred from the database. Each call is
one transaction. See the guide for opt-in comments, execution grants, supported
signatures, RLS and fresh-restart requirements.

For retry-safe note creation, the starter adds `create_note_with_tags_once`.
Retain one UUID and the same payload for a logical operation, then explicitly
replay with that key after an uncertain network outcome. Keys are user-scoped;
changed payloads return 409. The guide describes execution grants, receipt
retention, snapshot responses and deletion semantics.

Capture response diagnostics without wrapping fetch:

```ts
const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  accessToken: () => auth.getAccessToken(),
  onResponse: ({ requestId, status, durationMs }) => {
    console.info({ requestId, status, durationMs })
  },
}).schema('api')
```

`DataResponseInfo` is a readonly, runtime-frozen object containing only those
three fields. `requestId` is null when the header is missing, not exposed by CORS,
or not a UUID v4. No token, URL, query, headers, body or application identity is
passed to the callback. The SDK emits no logs unless your callback does so.

The callback runs once per HTTP response, including errors and individual retry
attempts, across reads, writes and RPCs. Duration is monotonic client time from
token lookup until fetch returns response headers; it excludes body consumption
and is not database execution or platform wake timing. The callback cannot read
or consume the response body. Network failures, cancellation before headers and
invalid tokens produce no callback when there is no HTTP response.

Callbacks may be synchronous or asynchronous. Exceptions and rejected promises
are ignored, and asynchronous completion is not awaited. Keep synchronous work
short; the request does not wait for asynchronous telemetry to finish. The hook
does not change retries, data, errors, counts or generated schema inference.

### Runtime JSON validation

Use `validateJson` after checking the query error to validate selected fields.
It accepts synchronous objects with a `parse(unknown)` method, including Zod
schemas, and infers each field's result from the parser output. Validators are
application-owned; PostgreSQL JSON columns do not describe their internal shape.

```ts
import { validateJson } from '@gregale/data'
import { z } from 'zod' // install in your application

const { data, error } = await query
if (error) throw error
const rows = validateJson(data, {
  details: z.object({ count: z.number() }).nullable(),
})
```

Arrays and single rows are supported. A null single-row result stays null; field
nulls go through the parser, so accept them explicitly with a nullable schema.
The helper copies rows and preserves unvalidated fields. Nested JSON validation
belongs inside your parser. Async parsers are unsupported.

Failures throw `JsonValidationError` with code `JSON_VALIDATION_FAILED`, the
selected `field`, and a zero-based `rowIndex` (null for a single row). Original
parser messages, causes and payloads are omitted. Validation runs locally after
the request and does not change PostgREST errors or database authorization.

### Reusable cursor pagination

```ts
import { cursorPage, cursorFrom } from '@gregale/data'

const order = [
  { column: 'created_at', ascending: false, nullsFirst: false },
  { column: 'id', ascending: false, unique: true },
] as const
const { data, error } = await cursorPage(
  db.from('notes').select('id,created_at,body'),
  { order, size: 20 },
)
if (error) throw error
const after = data.length ? cursorFrom(data.at(-1)!, order) : undefined
// Supply after with the same order and filters on a fresh query for the next page.
```

`cursorPage` preserves the selected result type and supports table/view reads,
filters, cancellation and native errors. Use a fresh array-returning select
builder without existing ordering, range or limits. Include every ordering
column in the selected row to extract its cursor. Ordering columns must be
simple names with scalar string, finite number, boolean or null values; JSON,
embedded fields and expression ordering are unsupported. Dates use their wire
strings. Defaults are ascending and nulls last, explicitly applied at every key.

The final ordering column must be non-null and unique within the filtered
result. Mark it `unique: true`; the application is responsible for this database
invariant. The helper cannot infer constraints from client types. Cursor values
are validated and quoted for PostgREST filter syntax, including commas, quotes
and backslashes. Cursor tuples are neither signed nor authorization tokens.

Keep filters, ordering and identity consistent across pages. RLS runs on every
request. Deleting the cursor row does not invalidate continuation; newer rows
before the cursor do not shift subsequent pages. This is not a snapshot across
requests: inserts after the cursor can appear, deleted rows disappear, and
changing ordering values can move rows. Stop when a page is empty. Large integer
keys must be represented without JavaScript precision loss by your application.
Existing server row caps can return fewer rows than the requested page size.

To fetch the previous page, use `before` with the cursor of the first displayed
row and keep the original ordering and filters:

```ts
const previous = await cursorPage(
  db.from('notes').select('id,body').eq('subject', 'alice'),
  { order, size: 20, before: cursorFrom(firstDisplayedRow, order) },
)
if (previous.error) throw previous.error
const displayRows = [...previous.data].reverse()
```

`before` returns the nearest preceding rows in reverse traversal order. Reverse
the returned array to display it in the original order. Use the same declared
`order` to extract each cursor; for another backward request, use the last row
of the raw response. `after` and `before` are mutually exclusive and invalid
cursors fail before the query builder is changed. Neither direction changes
RLS authorization or provides a snapshot across requests.

### Request deadlines and cancellation

```ts
import { dataRequest } from '@gregale/data'

const controller = new AbortController()
const result = await dataRequest(db.from('notes').select('id,body'), {
  timeoutMs: 5000,
  signal: controller.signal,
})
// Call controller.abort() when the screen closes or the filters change.
if (result.error?.code === 'request_timeout') { /* deadline exceeded */ }
if (result.error?.code === 'request_cancelled') { /* caller cancelled */ }
```

`dataRequest` preserves the query's selected types and normal result shape.
The deadline covers asynchronous token lookup, transport and response-body
consumption. It disables automatic retries, clears its timer and removes its
caller-signal listener after completion. Server responses retain their original
status and error code. Cancellation and deadline failures have status `0` and
null data; use the normal result-returning query mode with this helper.

A client deadline stops waiting for a response. PostgREST does not guarantee
immediate SQL cancellation when the connection closes. Gregale's restricted SQL
role enforces a 15-second statement timeout; an already running query can occupy
a database connection until that limit. Cancelling a mutation does not prove it
rolled back: reconcile its outcome before retrying. The integration gate checks
that concurrent cancelled RPCs release their connections within the server
statement timeout and that subsequent reads succeed.

### Optimistic concurrency

Use a database-managed version column to prevent stale edits from overwriting a
newer value. The [concurrent document example](../../examples/data-api-concurrency/README.md)
contains a trigger that increments the version for every update and a typed
edit/delete client.

```ts
import { optimisticMutation } from '@gregale/data'

const result = await optimisticMutation(
  db.from('documents').update({ body: editedBody })
    .eq('id', document.id).eq('version', document.version)
    .select('id,body,version'),
)
if (result.outcome === 'applied') {
  document = result.row
} else if (result.outcome === 'conflict') {
  // Reload the visible row and let the user review their edits.
} else {
  // Preserve result.error and result.status; reconcile uncertain writes.
}
```

The helper preserves selected row types and disables automatic retries. Use
`delete()` with the same primary-key/version filters for conditional deletion.
A stale, missing, deleted or RLS-hidden row produces the same `conflict` outcome;
a transport/server error remains an `error`. Include all primary-key columns,
and always call `select()` with an array result. The helper classifies the
response; the SQL predicate and version trigger enforce concurrency. It cannot
undo a mutation that targeted multiple rows. Use normal result-returning queries
rather than `single()` or `throwOnError()` with this helper.

### Retrying uncertain writes safely

The [idempotent order example](../../examples/data-api-idempotency/README.md)
provides a typed `create_order_once` RPC backed by a subject/tenant/key unique
constraint and immutable stored response. Persist a UUID and the complete
operation payload before sending the first request, and reuse both after a
transport failure or deadline. Identical retries return the original row;
a changed total returns HTTP 409 with `PT409` and
`idempotency_payload_conflict`. A new key starts a new operation.

The example's typed client composes generated RPC arguments with `dataRequest`;
no generic retry wrapper or HTTP header can make arbitrary mutations idempotent.
The database transaction enforces the guarantee. See the example for permissions,
receipt retention and limitations on external side effects.

### Read-only mutation recovery

Use an application's receipt lookup RPC to check a pending operation before
deciding whether to retry its write. The starter's append-only
`0011_note_receipt_lookup.sql` migration installs `read_note_create_receipt`;
apply the normal owner RPC permission workflow, refresh the API and regenerate
types. The idempotent-order example similarly adds `read_order_receipt`.
These authenticated RPCs derive identity from request claims, compare the original
key and payload, and return the stored original response without executing a write.

Given a pending note saved before the first attempt, use the same API and signed-in
identity and retain its original key, body and tag array:

```ts
import { dataRequest, reconcileMutation } from '@gregale/data'

const response = await dataRequest(
  db.rpc('read_note_create_receipt', {
    idempotency_key: pending.key,
    note_body: pending.body,
    tag_names: pending.tags,
  }).select('id,body,subject'),
  { timeoutMs: 5000, signal },
)
const recovery = reconcileMutation(response, row =>
  row.subject === pending.subject && row.body === pending.body,
)
if (recovery.outcome === 'confirmed') {
  showSavedNote(recovery.row.id)
} else {
  // Keep the pending key/payload. Review conflicts or check again explicitly.
}
```

`reconcileMutation` classifies a completed lookup; it executes no query, polling
or retry. Its synchronous predicate binds the returned receipt to the original
operation and preserves projected row types. Confirm only the same API, subject,
tenant where applicable, key and full payload. The SQL lookup compares payload
fields that are not part of the returned projection. Use `validateRpcResponse`
first when the application also requires runtime response validation.

Results are `confirmed` with `row`, `payload_conflict` with the original error,
or `unresolved` with `reason` (`not_found`, `request_failed`, `invalid_receipt`,
`replay_expired`).
Status is preserved, as are request errors on unresolved failures. Malformed,
multiple or mismatched rows and predicate exceptions cannot confirm the write;
rejected data and predicate exceptions are omitted. Conflict classification
requires HTTP 409, `PT409` and `idempotency_payload_conflict` exactly.

No visible receipt can mean uncommitted work, a hidden operation or a purged
receipt. It never proves rollback and does not authorize a new write. Keep retry
explicit. For legacy contracts, purging ends the guarantee for that key and a
replay can create a duplicate. Confirmation describes the original committed
response, even if the business row was subsequently edited or deleted.

### Opt-in v2 replay expiry

The starter's `0012_note_replay_expiry.sql` adds
`create_note_with_tags_once_v2` and `read_note_create_receipt_v2`. Apply the
migration, owner RPC grants and schema refresh, then regenerate types before
using these functions. Legacy functions keep their original retention contract.

```ts
import { createReplayKeyV2, classifyDataError, dataRequest } from '@gregale/data'

// Save this entire operation under its original API and identity before sending.
const pending = {
  contract: 'v2', subject: session.subject,
  key: createReplayKeyV2(), body: 'hello', tags: ['work'],
}
const result = await dataRequest(db.rpc('create_note_with_tags_once_v2', {
  idempotency_key: pending.key, note_body: pending.body, tag_names: pending.tags,
}), { timeoutMs: 5000 })
if (classifyDataError(result.error, { status: result.status })?.category === 'replay_expired') {
  // Keep pending. Check the original receipt or resolve through business review.
  // Never generate a replacement key automatically.
}
```

Keys are UUIDv7 values using the client clock and Web Crypto randomness. V2 uses
the key's embedded timestamp plus **24 hours**, allowing at most **five minutes**
of future clock skew. Server wall time decides expiry, including after lock
waits; a slow client clock shortens the available window. Client timestamps are
not trusted to assert when an operation actually began. Preserve the exact key:
changing its timestamp or contract version creates another operation namespace.

Expired writes return HTTP 410, `PT410`, `idempotency_key_expired`, even after
receipt cleanup. `classifyDataError` exposes `replay_expired` with `reconcile`
guidance. The v2 lookup can still confirm a retained expired receipt; if missing,
the exact expiry response becomes `unresolved` / `replay_expired`. Expiry cannot
prove whether an earlier attempt committed. No helper retries or replaces keys.
Invalid UUID versions/variants return `PT400` / `idempotency_key_invalid`, and
timestamps beyond the clock allowance return `PT400` / `idempotency_key_in_future`.

The migration cleanup tool accepts `--contract v2` and deletes only receipts whose
key deadline has closed. Keep the fixed v2 window unchanged; a longer policy needs
a new version to avoid reviving purged keys. The guarantee covers this SQL
contract under an accurate database clock and retained database history. Owner
schema changes, restores that lose receipts and external effects need separate
recovery policies.

### Versioned updates and deletes

The starter's `0013_note_mutation_receipts.sql` adds opt-in conditional writes
`update_note_once_v2` and `delete_note_once_v2`. Both require a UUIDv7 operation
key, note ID and expected version. Update additionally accepts `note_patch` with
only non-null `body` text and/or integer `priority`. Apply migrations and owner
RPC permissions, refresh the API, regenerate types and install the paired SDK.

```ts
import { classifyVersionedMutation, createReplayKeyV2, dataRequest, reconcileMutation } from '@gregale/data'

// Persist args and original API/identity/contract/action before the first send.
const subject = session.subject
const args = {
  idempotency_key: createReplayKeyV2(), note_id: note.id,
  expected_version: note.version, note_patch: { body: 'edited' },
}
const matches = (row: { id: number; subject: string; version: number; body: string }) =>
  row.id === args.note_id && row.subject === subject &&
  row.version === args.expected_version + 1 && row.body === args.note_patch.body
const response = await dataRequest(
  db.rpc('update_note_once_v2', args).select('id,subject,version,body'),
  { timeoutMs: 5000 },
)
const outcome = classifyVersionedMutation(response, matches)

// For an uncertain write, check the original receipt explicitly.
const read = await dataRequest(
  db.rpc('read_note_update_receipt_v2', args).select('id,subject,version,body'),
  { timeoutMs: 5000 },
)
const recovery = reconcileMutation(read, matches)
```

`classifyVersionedMutation` classifies a completed write result, without executing
or retrying anything. Its synchronous predicate preserves selected types and
binds the snapshot to the original operation. Outcomes are `applied` with the
original row, `version_conflict`, `payload_conflict`, or `unresolved` with the
existing reconciliation reasons. Only HTTP 409 / `PT409` /
`idempotency_version_conflict` becomes `version_conflict`; generic conflicts do
not acquire this meaning. Original errors/status are preserved. An empty success
result stays unresolved. Expiry remains unresolved with reason `replay_expired`.

For delete, call `delete_note_once_v2` and recover with
`read_note_delete_receipt_v2`, using key, note ID and expected version. Its receipt
contains the deleted snapshot with that same version. Update returns the stored
post-update version. Both lookups are read-only and remain useful after subsequent
edits/deletion; replay returns the saved response without reapplying the mutation.

Stale, absent and RLS-hidden rows share the version-conflict response. Failed
mutations leave no success receipt and reserve no key. A conflict alone cannot
confirm an earlier uncertain write. Preserve the original operation and never
automatically replace its key or expected version. Updates/deletes share a key
namespace, independent of creation; changing action, target, version or patch for
a successful key returns a payload conflict. The fixed v2 expiry/cleanup contract
also applies. Full setup and maintenance are documented in
[Data API recovery](../../docs/data-api.md#versioned-update-and-delete-recovery).

### Bulk mutations with partial results

```ts
import { bulkMutations } from '@gregale/data'

const results = await bulkMutations(edits, edit =>
  db.from('documents').update({ body: edit.body })
    .eq('id', edit.id).eq('version', edit.version).select('id,body,version'),
  { batchSize: 100, concurrency: 2, timeoutMs: 5000, signal, onProgress },
)
```

Each item runs once in its own transaction. `batchSize` is a scheduling window,
not an atomic database batch. Results preserve input order and selected row
types, distinguishing `applied`, `conflict`, `error`, `uncertain`, `skipped`, and
`invalid`. Cancellation skips queued items and aborts active requests. Progress
contains aggregate counts only; observer failures do not interrupt execution.

Reconcile uncertain writes before retrying. Use idempotent RPCs or versioned
conditional mutations where needed. A single normal `insert([...])` request
retains its existing atomic semantics. See the
[bulk document example](../../examples/data-api-bulk/README.md) for full outcomes,
transaction boundaries and retry guidance.

### Classify errors for application recovery

```ts
import { classifyDataError } from '@gregale/data'

const result = await db.from('notes').insert(input).retry(false)
const failure = classifyDataError(result.error, { status: result.status })
if (failure?.category === 'unique_violation') {
  showValidationMessage('That value is already in use.')
}
```

The helper returns `null` for a null/undefined error. Otherwise it returns frozen
metadata with `category`, `recovery`, `code`, `status`, and the unchanged, typed
`original` error. It also accepts errors caught from `throwOnError()`; omit status
when no response status is available. It never executes a request or changes
retry settings. `.retry(false)` in this example disables the underlying client's
retries explicitly.

| Category | Recognized code or fallback | Recovery guidance |
| --- | --- | --- |
| `unique_violation` | SQLSTATE `23505` | `change_input` |
| `foreign_key_violation` | SQLSTATE `23503` | `change_input` |
| `lock_conflict` | SQLSTATE `55P03` | `wait` |
| `deadlock` | SQLSTATE `40P01` | `wait` |
| `authentication` | `token_required`, `token_invalid`, `PGRST301`–`PGRST303`, HTTP 401 | `authenticate` |
| `permission_denied` | SQLSTATE `42501`, `origin_not_allowed`, HTTP 403 | `check_permissions` |
| `contract_mismatch` | `schema_contract_mismatch` | `refresh_contract` |
| `timeout` | `request_timeout`, `query_timeout`, `request_deadline_exceeded`, `data_api_sql_deadline_exceeded`, HTTP 504 | `reconcile` |
| `cancelled` | `request_cancelled`, SQLSTATE `57014` | `reconcile` |
| `response_too_large` | `response_too_large` | `reconcile` |
| `rate_limited` | `data_api_rate_limited`, HTTP 429 | `wait` |
| `concurrency_limited` | `data_api_concurrency_limited` | `wait` |
| `unavailable` | `data_api_unavailable`, `data_api_overloaded`, `serving_contract_unavailable`, `data_api_refresh_unavailable`, HTTP 503 | `check_service` |
| `no_response` | Status 0 with no recognized code | `reconcile` |
| `unknown` | Other errors | `inspect` |

Recognized codes take precedence over HTTP fallback. Messages, details and hints
never determine the category. Generic 409/`PT409` errors do not establish a
contract mismatch; SQLSTATE `57014` establishes cancellation, not its cause.
Pass the response status to classify SQL `PT503` maintenance errors as unavailable.
Native exceptions without a recognized code or supplied status remain unknown;
use `dataRequest()` for stable deadline/cancellation codes.

Recovery values are suggestions, not instructions to retry. Timeouts,
cancellation, response-size limits, missing responses, and service failures do not prove a mutation
rolled back. Reconcile writes or use an application-owned idempotency contract
before replaying them. `refresh_contract` means review the rollout and regenerate
appropriate client types; it does not automatically change a pinned fingerprint.
An empty RLS-filtered result is not a permission error. The preserved original
error and its code can contain application or database information: use curated
UI messages and do not blindly log or display the classification object.

New managed Data API logins default to a five-second lock timeout and retain the
15-second statement timeout. `55P03` also covers NOWAIT refusal, so the category
does not prove which lock policy fired. An uncaught contention error rolls back
the failing request's transaction, including earlier transactional RPC writes;
sequences and external effects are excluded. It cannot establish whether an
earlier attempt committed. Wait and review the whole operation before explicit
retry, using its idempotency/receipt contract when needed. Normal and throwing
CRUD/RPC errors keep their original code and private details; neither category
causes automatic replay. Existing roles and open pools require the documented
credential-policy upgrade and reconnect workflow; an SDK update does not alter
SQL defaults. Pinned PostgREST 14.3 already retries deadlocks and serialization
failures within its SQL transaction machinery. A deadlock may resolve and return
success from a single HTTP request; the SDK classification applies if `40P01`
actually reaches the caller and does not disable or extend engine retries.

An owner can opt the updated runtime into SQL deadlines across internal transaction
retries after installing the matching database hooks. The fixed gateway expiry
caps each attempt's remaining SQL budget. A hook that observes expiry returns
`data_api_sql_deadline_exceeded`, classified as `timeout` / `reconcile`, including
`throwOnError()` errors. A main statement interrupted by PostgreSQL can instead
return `57014`; its category remains `cancelled`. An invalid/missing hook contract
is a server setup failure, not evidence of mutation success. The SDK adds no retry
or database installation behavior. Review the original operation/receipt before
explicit replay; HTTP cancellation alone cannot establish SQL completion.

The updated runtime limits normal REST/RPC bodies to 16 MiB, independently of
row count. Known oversized lengths return `502 response_too_large`; overflow
after headers closes the incomplete body and can instead produce status 0 /
`no_response`. Generic HTTP 502 errors remain `unknown`. Use `onResponse` to
retain the header request ID before body consumption and correlate gateway logs.
Reduce future projections/page sizes or RPC output, and reconcile writes before
replay: SQL can commit before the result is rejected. The SDK adds no retries.

Optional gateway user-rate limits return `429 data_api_rate_limited` with
`Retry-After: 1`. Budgets follow verified subjects across token rotation and
routes, and are local to each runtime. The stable gateway code establishes a
rejection before SQL dispatch; generic HTTP 429 alone does not. Recovery `wait`
means defer the next request decision; it adds no timers or automatic retries,
including for ordinary GET/POST queries and `throwOnError()`. Preserve uncertain
write reconciliation when the error comes from another service or SQL.

Optional `DATA_API_USER_CONCURRENCY` limits concurrent authenticated requests per
verified subject within one runtime. Rejections use
`429 data_api_concurrency_limited`, classified as `concurrency_limited` / `wait`.
Their `Retry-After` is advisory (one second for active work, up to 15 for uncertain
dispatch cooldown). The rejected call did not reach SQL, but an earlier call may
still be running or may have committed after cancellation. Review that earlier
outcome separately. The SDK adds no retries, timers or automatic mutation replay;
generic HTTP 429 continues to fall back to `rate_limited`.

### Validate RPC arguments at runtime

Use `validatedRpc()` when inputs come from forms, JSON or JavaScript. Supply a
synchronous application-owned parser that returns the generated RPC argument
object, then construct the typed query in the callback:

```ts
import { validatedRpc, classifyDataError } from '@gregale/data'

// Example approved RPC: create_note({ body: string }) -> note ID.
const noteArgs = {
  parse(value: unknown): { body: string } {
    if (!value || typeof value !== 'object' || !('body' in value) ||
        typeof value.body !== 'string' || !value.body.trim()) {
      throw new Error('Body is required')
    }
    return { body: value.body.trim() }
  },
}

const outcome = await validatedRpc(formInput, noteArgs,
  args => db.rpc('create_note', args).retry(false))
if (outcome.outcome === 'validation_failed') {
  showValidationMessage('Enter a note body.')
} else {
  const { data, error, status } = outcome.result
  const failure = classifyDataError(error, { status })
  // data retains the generated create_note return type.
}
```

A compatible schema's `.parse()` method, such as a synchronous Zod parser, can
replace `noteArgs`. The parser owns required fields, unknown-key policy, defaults,
transformations and field-level UI validation. It must return an argument object;
null, arrays, primitives and asynchronous outputs are rejected. Empty objects
support zero-argument RPCs. Generated types check parser output against the chosen
RPC, including required arguments and value types. Select the appropriate schema
on `db` before calling the helper.

Invalid input returns `{ outcome: 'validation_failed', error }` with a
`RpcValidationError` and code `RPC_ARGUMENT_VALIDATION_FAILED`. No query callback,
token lookup or request runs. The error excludes input values and original parser
exceptions; obtain field feedback separately through your application's parser,
using its own disclosure policy.

Valid input returns `{ outcome: 'completed', result }`. Completed means the
callback was awaited, not that the database operation succeeded: inspect
`result.error` as usual. The helper preserves the original result by reference,
including null results and server/transport errors. `throwOnError()` and callback
exceptions propagate unchanged. The callback can apply projections, filters,
`.single()`, abort signals or `dataRequest()` without losing result inference.

The helper does not change retry settings. `.retry(false)` above explicitly
prevents automatic retries; write reconciliation and idempotency remain
application responsibilities. PostgreSQL constraints and RLS remain authoritative.
The helper does not clone parser input or output; application parsers own any
mutation they perform.

### Validate entire RPC responses

Generated declarations describe expected responses; they do not validate received
JSON. Use `validateRpcResponse()` with a synchronous application-owned parser
after awaiting a direct RPC or a completed `validatedRpc()` result:

```ts
import { validateRpcResponse, classifyDataError } from '@gregale/data'

// Example approved scalar RPC: count_notes() -> integer.
const result = await db.rpc('count_notes', {}).retry(false)
const checked = validateRpcResponse(result, {
  parse(value: unknown): number {
    if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
      throw new Error('Invalid count')
    }
    return value
  },
})

if (checked.outcome === 'validated') {
  const count: number = checked.result.data
} else if (checked.outcome === 'request_failed') {
  const failure = classifyDataError(checked.result.error, {
    status: checked.result.status,
  })
} else {
  // RPC_RESPONSE_VALIDATION_FAILED; the original HTTP status is retained.
  showValidationMessage('The server returned an unexpected result.')
}
```

The parser checks the entire `data` value: a scalar, JSON object, projected row or
row array. It owns validation, transformations, nullable handling and cloning.
Synchronous schema parsers such as Zod's `.parse()` are compatible. Null or
undefined successful data is passed through the parser, not silently skipped.
Async parser outputs are rejected. To validate individual JSON fields in an
already checked row result, the existing `validateJson()` remains available.

`validated` returns a shallow copy of the response with parser output in `data`
and `error: null`. The parser output determines the new data type; status, count
and other response metadata retain their original types and values.
`request_failed` returns the original response by reference without parsing when
it has an error or a non-2xx status, including transport/deadline failures.
`validation_failed` returns a `RpcResponseValidationError` with code
`RPC_RESPONSE_VALIDATION_FAILED` and the original HTTP status. It excludes rejected
data and the original parser exception. Its HTTP status can still be 200.

This helper executes no requests and changes no retry settings. A response
validation failure does not imply rollback: a mutation may already have committed.
Reconcile through reads or a reviewed idempotency contract before considering
another write; never automatically replay on a validation failure. Raw request
failure results may still contain private database errors. Use curated UI messages.
The helper does not mutate the original response itself, but parsers receive its
data by reference and are responsible for any mutation they perform.

### Encrypted continuation tokens for server routes

Existing `cursorPage`/`cursorFrom` helpers use raw ordering values. For routes that
accept an untrusted continuation token, use `createCursorPager` on the server:

```ts
import { createCursorPager } from '@gregale/data'

// Load a stable 32-byte random key from server-side configuration. Never send it
// to a browser or commit it. Reuse the pager across requests on this server.
const pager = await createCursorPager({ key: cursorKeyBytes, ttlMs: 15 * 60_000, maxPageSize: 100 })
const order = [
  { column: 'created_at' },
  { column: 'id', unique: true },
] as const

// Derive scope from verified application identity/tenant/permission claims.
const page = await pager.page(
  db.from('orders').select('id,created_at,total').eq('team_id', authenticatedTeamId),
  {
    order, size: 50,
    scope: { identity: trustedIdentityScope, schemaFingerprint: currentSchemaFingerprint },
    after: incomingContinuation,
  },
)
// Return page.result and page.nextCursor to the caller. API errors remain in result.
```

Ordering columns must appear in the selected projection and have scalar types.
A single primary key works with `[{ column: 'id', unique: true }]`; timestamp-plus-ID
ordering uses the example above. The final column must actually be non-null and
unique within the filtered result. The helper validates the declaration and values,
but does not inspect database constraints. Use a fresh GET array query with no root
order, limit, offset or range applied beforehand. Nested projection types are preserved.

Tokens use authenticated AES-GCM encryption with fresh random nonces. They bind the
actual query endpoint, schema, filter/projection parameters, ordering, page size,
identity scope and schema fingerprint. Refreshing a JWT need not change the scope;
include tenant and relevant permission/version claims in a server-derived scope.
Create the data client with `expectedSchemaFingerprint` matching the scope so the
gateway also rejects schema changes before executing a read. The scope alone does
not detect an unannounced gateway migration. The helper does not authenticate callers,
read claims from JWTs or replace RLS.
Keep the key exclusively on trusted servers. A browser should submit issued tokens
to your route; browser-only clients can continue using the raw keyset helpers.

The default TTL is 15 minutes (maximum one day), and default maximum page size is
999. One additional row is fetched to determine continuation within Gregale's
1,000-row response cap. Each page returns at most the requested size, with
`nextCursor`, `previousCursor`, `hasMore` and `direction`. `hasMore` describes the
current traversal direction. `before` returns nearest preceding rows in reverse
order, as with `cursorPage`; use `previousCursor` to continue backward and reverse
rows for normal display. Empty/error results return no continuations. First forward
pages have no previous cursor. Continuations describe boundaries, not guaranteed
nonempty pages if records are subsequently deleted.

Malformed/tampered tokens fail with `CursorPaginationError.code = 'invalid_cursor'`.
Expired tokens use `expired_cursor`; changed query, scope, ordering, page size or
schema uses `incompatible_cursor`. These failures occur before query execution.
Invalid options or malformed successful responses have separate codes. Key rotation
invalidates existing tokens; restart pagination rather than attempting an automatic
fallback. Tokens are bounded to 16 KiB and ordering to 16 columns. Cursor values
and filters are not exposed in plaintext tokens. Pagination does not provide a
snapshot across requests, and changing ordered values can still move rows between
pages. Index the filter and ordering columns and review plans with representative data.

### Validating atomic batch receipts

For the starter's opt-in v2 batch RPC, use its `prepareNoteBatch` and
`noteBatchMatches` helpers with the paired generated types. `classifyVersionedMutation`
and `reconcileMutation` accept synchronous type guards, so validating the entire
one-row envelope narrows its nested JSON to typed ordered snapshots. Every item
must match before the result becomes `applied` or `confirmed`; classify completed
responses only. These helpers send no requests or retries.

The explicit RPC commits 1–100 distinct note updates/deletes atomically and
stores one receipt; SDK `bulkMutations` retains its separate-request semantics.
Retain the original API, identity, key, contract and ordered operation array for
read-only recovery. Expiry, absence and conflicts cannot alone resolve an earlier
uncertain request. See the [batch contract and example](../../docs/data-api.md#atomic-note-batch-recovery).

### Durable mutation journals

`createMutationJournal` optionally saves an original prepared operation through
an application-supplied atomic store and synchronous lossless parser. Provide
its exact API URL, optional schema fingerprint, subject, tenant (or null), and
contract. Operations must include that context, one canonical UUIDv7 `key` and
the complete captured payload. The parser must return identical canonical JSON;
restoration cannot silently drop fields or replace a key. The generated v2
transaction client supplies this parser and an easier persistent wrapper.

Use `createIndexedDBMutationStore({databaseName:'my-app-data-mutations'})` in a
browser to opt into a dedicated, bounded store with strict transaction durability.
It opens lazily. `journal.prepare(slot,operation)` inserts only into an empty
stable slot. `journal.load(slot)` validates and freezes saved data without a
request. `journal.dispatch(entry,send)` commits uncertainty before invoking the
explicit callback once, returning its new `entry` and original `response`.
Competing tabs cannot claim the same prepared revision. Storage failures prevent
sending; callback errors leave the uncertain operation available through load.

`journal.reconcile(entry,response,matches)` classifies a completed response using
the existing receipt contract/type guard. `journal.recover(entry,lookup,matches)`
explicitly calls a receipt-read callback and classifies it. Neither performs
retries or key replacement. They return `result`, `entry` and `persistence`:
`saved`, `unchanged`, `conflict` or `storage_failed`. Proven server success remains
in `result` even if a local confirmation marker cannot be committed. Empty,
expired, conflicting and malformed receipts keep the original operation.

Only `journal.removeConfirmed(entry)` explicitly removes local records, with
an atomic revision check. It cannot clear uncertainty or delete newer work from
a reused slot. Handles belong to one journal instance; load new ones after
restart. API/identity/tenant/contract/fingerprint mismatches and invalid records
fail closed without returning or changing the saved operation.

Default bounds: 100 IndexedDB records, 8 MiB stored values, 1 MiB per record,
256 UTF-8 bytes per slot/database name and JSON depth 64. Count/byte budgets may
be lowered. No automatic pruning, background polling or fallback storage is
introduced. Applications intentionally persist payloads; their parser should
allow only the prepared operation and exclude credentials/request options.
Origin storage must remain available for recovery; clearing/evicting it or losing
the device removes that local copy. A custom `MutationJournalStore` must implement
durable atomic `compareExchange(slot,expected,next)` across its users; separate
localStorage reads/writes do not provide that guarantee.

Errors use `MutationJournalError.code` with curated messages: storage_unavailable,
storage_full, conflict, invalid_record, invalid_operation, context_mismatch,
invalid_entry, not_prepared and not_confirmed. Original storage/parser exceptions
and rejected payloads are omitted. See the [generated v2 integration](../../docs/data-api.md#durable-pending-operation-recovery).
