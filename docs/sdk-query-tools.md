# Typed query tools and authorized resources (SDK preview)

Node and Python expose existing application reads through the same typed contracts
used by Jobs. A query can be called from an application, a pipeline handler or an
authenticated MCP host. It does not start a workflow or require a durable write
receipt. Existing pipeline tools, approvals and outbound actions keep their APIs.

## Define and read

Use a generated `JobContract<Input, Row>`: its input describes the approved query
arguments and its output describes one result row. No SQL, credentials, owner
claims or remote URLs are accepted from the model. The application supplies a
fixed handler and current authorization:

```ts
import {defineQueryTool} from '@gregale/sdk-node/preview';

const rows = defineQueryTool(contract, {
  name: 'order_rows', description: 'Search rows from an owned order.',
  revision: 'v1', scope, cursorKey: process.env.QUERY_CURSOR_KEY!,
  authorize: authorizeCurrentOrder,
  load: async (input, {actor, after, limit, signal}) =>
    readOwnedRows(input, actor.owner_id, after, limit, signal),
});
const page = await rows.page(input, session, {timeoutMs: 5000, pageSize: 20});
for await (const row of rows.items(input, session,
    {timeoutMs: 30_000, maxPages: 10, maxItems: 200})) useRow(row);
```

```python
from faas_sdk.preview import QueryTool

rows: QueryTool[Input, Row, Session] = QueryTool(
    contract, name="order_rows", description="Search rows from an owned order.",
    revision="v1", scope=scope, cursor_key=cursor_key,
    authorize=authorize_current_order, load=read_owned_rows,
)
page = await rows.page(input, session, timeout=5, page_size=20)
async for row in rows.items(input, session, timeout=30, max_pages=10, max_items=200):
    use_row(row)
```

`authorize(input, access, signal)` returns `{subject_id, owner_id}` or null. Python
returns `QueryActor`, and its third argument is a synchronous `check_cancelled`
function. Identity fields use 1–128 ASCII letters, digits, `_`, `.`, `:`, or `-`.
`load` returns exactly `{items, after}`; `after` is an opaque backend string or
null at the end. Python receives `QueryLoadContext(actor, after, limit,
check_cancelled)`. Handlers must supply a deterministic order and a matching
continuation position. The SDK validates every row and rejects extra page fields,
oversized pages, empty continuation pages and repeated positions.

Authorization runs before and after each load, and must return the same current
subject and owner. Enforce ownership in the actual database query/RLS as well:
an authorization callback cannot scope an otherwise unrestricted SQL connection.
Use a restricted read-only database role or a read-only Data API RPC. The CSV
example additionally executes each fixed SQL handler in a read-only transaction
with a statement timeout. The SDK cannot make an arbitrary callback read-only.

## Cursors, limits and cancellation

`page` returns `{items, next_cursor}`. Pass `after: page.next_cursor` to continue.
The cursor is a portable HMAC-SHA256 envelope binding the query input, generated
contract, app/account/API scope, definition name/revision and current subject/owner.
Its backend position stays a string; large integer IDs and precise timestamps
are never converted to JavaScript numbers or Python datetimes. Cursors contain
no raw query input or principal, but the backend position is encoded, not encrypted.
Do not place secrets in that position. A cursor is never permission to read data.

Use the same backend-only 32–128 printable ASCII character key in both languages.
Generate it securely and retain it in application secret configuration; never
include it in MCP arguments or browser code. Rotation invalidates outstanding
cursors. Bump the revision when query semantics or ordering change. Changing the
principal, input, scope or contract requires a new first page. Pages are live
reads; concurrent edits may move rows between pages. This is not a snapshot.

`pages` and `items` are lazy. Their deadline starts on the first pull and includes
time paused between pulls. Each page reauthorizes current access; there is no
shared result cache. Iteration stops at `maxPages`/`maxItems` without an extra
query. Use `pages` when the last cursor must be saved for continuation.

Defaults are 20 rows/page, 100 rows maximum, 256 KiB/page, 10 pages and 1,000
items/iteration. Definitions may lower the page cap, or choose a response cap up
to 1 MiB. Iterations allow at most 1,000 pages and 100,000 items. Input is at most
8 KiB, backend position 2 KiB and public cursor 8 KiB. These are SDK bounds;
existing native request/plan/database limits remain authoritative.

One total deadline covers authorization and loading. Node accepts AbortSignal;
Python preserves asyncio task cancellation. Late callback completion is fenced
and cannot return rows after the deadline. Handlers should honor cancellation
and configure remote statement/request timeouts; local cancellation does not
guarantee cancellation of an already running remote query. No automatic retries
or mutation path are added. Errors expose stable `QueryToolError.code` values
without private database/transport exception messages.

## Resources and MCP

`defineQueryResource` / `QueryResource` binds a name, revision, app scope, fixed
MIME type, current authorization callback and bounded UTF-8 text loader. It
provides `link(id, access, options)` and `read(uri, access, options)`. IDs use
1–128 ASCII letters, digits, `_` or `-`; the loader maps that ID to an owned
application record, never to a caller-supplied path or URL. Reads reauthorize
before and after loading, validate UTF-8 and apply the byte budget. File-backed
loaders should verify their saved artifact digest, as in the CSV example.

```ts
const mcp = createQueryToolMCP(rows, {
  resources: [reports],
  links: row => [{resource: reports.name, id: row.report_id}],
});
// Register mcp.tools with tools/list, mcp.call with tools/call,
// mcp.resourceTemplates with resources/templates/list,
// and mcp.readResource with resources/read in the authenticated host.
```

Python uses `QueryToolMCP(tool, resources=[reports], links=...)`, `.tools`,
`.call(name, args, principal, timeout=...)`, `.resource_templates`, and
`.read_resource(uri, principal, timeout=...)`.

Each tool accepts `{input, after?, page_size?}` with no extra fields. Results
include structured pages, their JSON text equivalent, and optional deduplicated
`resource_link` blocks. Every linked resource is separately authorized. Resource
links are identifiers, not bearer URLs, and need not appear in `resources/list`.
Register the resources capability only when serving resources; subscriptions and
list-change notifications are not implemented. The host owns authentication,
transport, initialization, protocol negotiation and JSON-RPC encoding. See the
[MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
and [resources](https://modelcontextprotocol.io/specification/2025-11-25/server/resources)
specifications.

Default protocol compatibility is `2025-11-25`; host-selected `2026-07-28` adds
`resultType: 'complete'` to tool results. Tool arguments are capped at 16 KiB,
complete responses at 1 MiB, registered resources and returned distinct links at
100. JSON-RPC unknown tool/invalid arguments use -32601/-32602. Tool execution
failures return `isError` with a sanitized code. Resource reads return -32002 for
unavailable/unauthorized identities and -32603 for other failures. Denied reads
never return partial content. Treat returned application text as untrusted data.

## Evidence and migration

This is an additive preview API: no native endpoint, platform migration or
automatic application DDL is added. Shared Node/Python fixtures pin contracts,
cursor bytes, descriptors and resource identities. Tests cover concurrency,
revocation during/between reads, limits, cancellation, total deadlines and
current resource access. Installed npm/wheel consumers check generic types.

The [CSV example](../examples/sdk-csv-jobs/README.md#query-context-before-recovery)
searches owned rows, reads a verified failure summary and current payment data,
then continues into the existing human-approved cleanup and refund journey. Its
PostgreSQL and Chrome are real; native workflows and Stripe use protocol fixtures.

For bounded scans that may produce a sparse page, opt in with
`allowEmptyPages` / `allow_empty_pages`. Consumers can continue an empty page
using its cursor. Use `defineAuthorizedQueryTool` or Python's `load_authorized` callback when
composing another currently authorized reader. That callback receives the
original application principal and a context with the remaining deadline.
