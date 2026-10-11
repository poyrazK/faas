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
