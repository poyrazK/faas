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
See [the Data API guide](../../docs/data-api.md).

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
