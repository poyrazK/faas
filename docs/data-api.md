# Schema-generated Data APIs

Data APIs expose PostgreSQL relations as authenticated REST endpoints and
generate TypeScript application types from the same schema. This is a managed
PostgreSQL preview: your account and region must already be enabled for managed
databases, `data_api` bindings, port 5432 egress, and app tasks.

For a complete application starting point, run
`gregale init --template data-api-starter --path notes`. The
[starter README](../cmd/gregale/templates/data-api-starter/README.md) covers
versioned release migrations, two-user RLS, SDK tarball installation, a typed
client, `data-api sync` and application CI. The migration app and generated API
remain ordinary, separately deployed apps.
Use [CLI/SDK bundles](data-api-packaging.md) to package a matching pair,
pin their checksums in the application, and restore the SDK in client CI.

## Create an API

Start with a ready managed database. Use your application's identity provider
issuer, JWKS URL and audience. Its tokens must have `sub` and `exp` claims and
use RS256 or ES256 signatures.

```sh
gregale data-api create notes-data --database notes-db \
  --issuer https://identity.example.com/ \
  --jwks-url https://identity.example.com/.well-known/jwks.json \
  --audience notes --origins https://app.example.com
```

The command reserves an ordinary app, attaches a restricted `DATABASE_URL`
binding, seals the authentication settings, enables plan-gated PostgreSQL egress
and uploads the Data API template through the normal builder/deploy path.
Creation and `--resume` set `require_authn=false` and change owner-key
`public_auth.mode=bearer` to `open` so application JWTs reach the runtime verifier.
Other explicitly configured ingress restrictions remain in place on resume.
The runtime still requires a valid application token for data requests.
It creates the `api` schema if needed. An empty schema yields an API with no
table endpoints. `--scope` selects the binding, secret and deployment scope.
If configuration or deployment fails, the app remains available for inspection.
Repeat the command with the same settings and `--resume` to continue; the binding
request uses a stable idempotency key. Use ordinary app/database commands to
inspect or remove retained resources.

## Define your application schema and authorization

Create tables and policies with your existing migration tool, using a managed
`migration` binding. Those credentials are delivered only to release tasks;
the serving Data API and type-export task cannot perform DDL. See
[managed migration credentials](managed-postgres.md#safety-boundary).
The migration owner automatically grants current and future `api` tables to
the restricted Data API login. Tables in `public` are excluded.

For example, apply this migration as the stable schema owner:

```sql
CREATE TABLE api.notes (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject text NOT NULL,
  body text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_notes ON api.notes
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');
```

Every valid application token uses the same restricted SQL role, with the
original JWT subject and claims available to RLS. A token's external `role`
claim cannot select an administrator. A table without RLS grants every
authenticated user the binding's table access, so define policies before
exposing user-specific data. On PostgreSQL 15+, use `security_invoker=true`
for exposed views that must follow the underlying table's RLS; ordinary views
can run with their owner's privileges. Only explicitly opted-in security-invoker RPC functions are exposed; see the RPC section below.

## Generate types and use the client

After deploying a live Data API revision:

```sh
gregale data-api types notes-data --output src/database.types.ts
gregale data-api types notes-data --output src/database.types.ts --check
```

The owner-authenticated command runs a bounded task on the app's current live
deployment. It reads a consistent database catalog snapshot using the
restricted runtime binding. It exports tables, views, enums, domains, arrays,
nullability, defaults, generated columns and foreign-key relationships.
Generated `Insert` and `Update` types exclude generated-always fields.
Unrecognized PostgreSQL relation types become `unknown`. Supported opted-in
RPC signatures are generated; arbitrary composite types are not. Numeric types become JavaScript `number`, so values beyond
JavaScript's exact numeric range need an application-specific representation.

`@gregale/data` is packaged in `sdk/data` and follows the existing
[SDK publishing policy](../sdk/README-publishing.md). It has not been published
to npm by this change. Build and pack it from an authorized checkout with
`npm ci && npm run build && npm pack` in that directory, then install the tarball
in your application.

```ts
import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

const db = createDataClient<Database>({
  url: 'https://notes-data.gregale.dev',
  accessToken: async () => session.getAccessToken(),
}).schema('api')

const { data, error } = await db.from('notes')
  .select('id,body,created_at').order('created_at', { ascending: false }).range(0, 19)

await db.from('notes').insert({ subject: session.user.id, body: 'Hello' })
```

The token getter is evaluated for each request, supporting session renewal.
Use application access tokens. Gregale owner keys manage the app and export
types; they do not authenticate its data endpoint. The client supports
PostgREST filters, projections, pagination and relationship queries.

## Refresh after a migration

```sh
gregale data-api refresh notes-data --wait --timeout 5m
gregale data-api types notes-data --output src/database.types.ts
# Run your application's TypeScript checks before deploying the new client.
```

Refresh requests the existing fresh app restart, which rebuilds the schema
cache. It may interrupt in-flight requests. With `--wait`, success requires the
accepted restart to complete and the Data API's public HTTPS `/healthz` to return
`{"ready":true}`. The CLI uses the app's canonical URL, falling back to its
platform URL, and sends no account credentials to the health endpoint. Your
ingress settings must allow the CLI to reach it.

One deadline covers app lookup, restart admission, completion and readiness.
It defaults to five minutes; `--timeout` accepts a positive duration up to one
hour and requires `--wait`. Failure or timeout exits nonzero. Timing out stops
the local wait; an accepted restart continues. `--json --wait` emits one receipt
with the `wake_id`, `status: "completed"` and `ready: true` only after both checks
pass. Without `--wait`, success still confirms restart admission only.

Use backward-compatible migrations during rollouts. Generate types after a
successful refresh, then run your application's type checks. Keep
`gregale data-api types notes-data --output src/database.types.ts --check` in
application CI to detect schema drift. Type generation and `--check` read the
current database; neither command refreshes a live PostgREST cache.

## Typed relationships

Type export includes declared foreign keys between readable tables in the same
exported schema, with ordered composite columns and one-to-one metadata. The
typed client uses these to infer nested PostgREST projections. In the starter:

```ts
const notes = await db.from('notes')
  .select('id,body,comments(id,body),note_details(summary)')
const comments = await db.from('comments').select('id,body,notes(id,body)')
```

`comments` is an array. A reverse one-to-one `note_details` embed is an object
or null. A comment's nullable `note_id` makes its parent `notes` embed nullable;
`notes!inner(id,body)` removes rows without a visible parent. Matching
primary-key or unique **constraints** determine one-to-one cardinality, following
PostgREST; a standalone unique index does not set that metadata. Constraint
changes affect the schema fingerprint and are detected by `types --check`.

Cross-schema foreign keys are omitted from generated relationship metadata:
the client resolves embedded types within the selected schema, so exporting
such a relationship could infer a same-named, unrelated local table. View-inferred
and computed relationships are not emitted. The starter qualifies many-to-many
joins in both directions using `tags!note_tags(id,name)` and
`notes!note_tags(id,body)`. The SDK infers these from the junction foreign keys;
PostgREST requires their columns to be included in the junction primary key.
The starter also qualifies a second `note_favorite_tags` path: ambiguous
unhinted embeds fail with `PGRST201` (HTTP 300), and junction hints select the
ordinary or favorite path explicitly. Ambiguity and invalid hints are runtime
PostgREST errors; the SDK does not validate junction hints at compile time. Each joined table needs its own RLS
policy. RLS can hide a parent even
when its FK column is NOT NULL, so handle missing embeds at runtime or use
`!inner` when a visible parent is required; static nullability follows the FK
columns. The starter uses subject-bound composite FKs to prevent cross-user
attachments as well as subject RLS on every table.

## Automate migrations and client validation

Commit a `data-api.json` workflow alongside your application:

```json
{
  "output": "web/src/database.types.ts",
  "migrate": {
    "directory": "migrations",
    "command": ["gregale", "deploy", "--name", "notes-migrations", "--wait", "--timeout", "15m"]
  },
  "check": {
    "directory": "web",
    "command": ["npm", "run", "typecheck"]
  }
}
```

The migration app must already have a ready `migration` binding and a Procfile
`release:` command or manifest `release.command`. `gregale deploy --wait`
returns success only after the release task succeeds and the deployment becomes
ready. Configure PostgreSQL egress on that app as described in the
[migration setup](managed-postgres.md#safety-boundary). The Data API app and its
restricted binding must also be deployed first. Install the client project's
dependencies and create the output directory before running the workflow.

```sh
gregale data-api sync notes-data --config data-api.json --timeout 20m
```

Sync validates the complete configuration and resolves the Data API's HTTPS
health URL, then runs four steps in order: the migration command, a fresh restart
with completion and readiness checks, private type generation with an atomic
file write, and the client check. A failed step exits nonzero and prevents later
steps. Invalid or truncated type output leaves the existing file intact. A
failed client check retains the new types so you can fix the application.

Commands are explicit argument arrays, executed locally with your environment
and no implicit shell. Use your existing migration tool or a deployment script;
the migration command must wait for successful completion, rather than return
after queueing background work. Managed migration credentials stay inside the
release task. Keep credentials out of the workflow file and command arguments.
All relative paths resolve from the workflow file's directory; each command's
optional `directory` defaults to that directory. A relative executable path
resolves from its command's directory. Unknown JSON fields, missing commands,
unavailable executables and invalid directories fail before migrations start.

One deadline covers the workflow, defaulting to 20 minutes with a maximum of
one hour. Interrupting or timing out stops the local wait and commands; accepted
remote deployments, restarts or tasks may continue. On Unix, local command
descendants are terminated as well. Sync does not undo committed migrations or
completed steps. Use replay-safe, backward-compatible migrations. Serialize
workflow runs for each database so another migration cannot change the schema
between refresh and export. If a later step fails, inspect the reported
wake/task IDs and use the individual refresh, types and client-check commands
to continue, or rerun a replay-safe workflow.

Command output goes to stderr. `--json` writes a single success receipt to
stdout only after the client check passes, including the app, wake ID, type
task/deployment IDs, schema fingerprint and output path. Use sync in migration
jobs. Keep `data-api types --check` in application CI as the read-only drift
check; it never runs the migration or refresh steps.

## HTTP contract and bounds

Relation CRUD lives at `/rest/v1/TABLE`. Supply `Authorization: Bearer JWT`.
Authenticated `/openapi.json` describes the generated API; `/healthz` reports
engine readiness. Browser origins must be explicitly allowed. The service
rejects missing, expired, wrong-audience and invalid tokens with HTTP 401.

V1 bounds each instance to two database connections, 1,000 returned rows,
1 MiB request bodies and a 15-second upstream timeout. Type generation is
bounded to 1,000 exposed relations, 10,000 columns, 20,000 catalog types,
and 1 MiB output. Truncated output fails without replacing your types file.
The normal app, task and database quotas and billing rules apply.

## Operator enablement and verification

Neon backend entries require `data_api_enabled: true`. Requalify the exact
backend configuration with the existing managed PostgreSQL qualification
workflow before enabling it for accounts. Qualification must prove schema
isolation, RLS, credential recovery, rotation and retirement. Keep the existing
managed PostgreSQL provisioning and task/release gates enabled as appropriate.
See the [preview runbook](managed-postgres-preview-runbook.md).

`make data-api-check` runs unit and TypeScript inference checks.
`make data-api-acceptance` additionally requires a disposable administrative
`DATA_API_TEST_DATABASE_URL` and the pinned PostgREST executable in
`DATA_API_POSTGREST_BIN`. The acceptance test owns a private database and login
and removes them on completion. Native VM/build deployment and live Neon
qualification are separate rollout checks; this change does not deploy services.

After qualification, run the opt-in [staging canary](../tests/data-api/staging/README.md)
with `make data-api-staging-canary`. It deploys disposable issuer, migration and
Data API apps through the remote builder, exercises a packed typed client,
checks parking/wake, schema migration/refresh and credential rotation, and
records cleanup evidence. `make data-api-staging-check` tests its harness locally;
those portable tests do not constitute live rollout evidence.

Pagination uses `notesClient.page({ offset: 0, size: 20, priority: 1 })`.
Offsets are zero-based and ranges are inclusive internally. Rows are ordered by
`created_at` descending, then `id` descending to break timestamp ties. The
optional priority filter applies before pagination. `data` contains the page;
`count` is the exact number of matching rows visible under the caller's RLS.
A filtered query with no matches returns an empty array and count zero.
An offset equal to the count returns an empty page with that count. An offset
greater than the count returns `PGRST103` (HTTP 416); reset the
offset if rows were deleted between requests. The server caps each response
at 1,000 rows even when a larger page is requested; its exact count still covers
all matching visible rows. Advance by the number of returned rows, and stop
when offset plus returned length reaches count. Counts and separate page
requests do not provide a shared snapshot: concurrent writes can shift offset
pages. Exact counts may be expensive on large tables. The `page()` method supports offset pagination; cursor pagination is shown
in the runnable example below.

For changing data, use `cursorPage({ size: 20 })` and derive the next `after`
value with `noteCursor(rows.at(-1))`. Pass that cursor to
`cursorPage({ after, size: 20 })`, keeping the same filters, until a page is
empty. The runnable example prints the first page and its next cursor.
Cursors contain the original `(created_at, id)` values; timestamps retain
PostgreSQL microseconds. Do not round them through JavaScript `Date` or change
ordering on the query. The descending query selects timestamps older than the
cursor, or lower ids with the same timestamp. Newer inserts do not shift the
continuation, and deleting the cursor row does not invalidate it. RLS applies
to every page; cursors carry no authorization. Malformed timestamps, invalid
integer ids and invalid page sizes fail locally before sending a request.
Cursor pages do not request exact counts. The existing server row cap still
applies, so continue from the last returned row even for a short page.
Each request sees current data: this is not a snapshot. Inserts behind the
cursor may appear, deleted rows disappear, and changing `created_at` can move
rows across the boundary. Applications needing immutable ordering should
prevent edits to that column through their database privileges or policies.

The starter's `0006_query_indexes.sql` adds ownership-prefixed indexes for
`(subject, created_at DESC, id DESC)` pagination and
`(subject, priority, created_at DESC, id DESC)` filtered pagination. A
`(subject, note_id)` index supports comment embeds and cascading note deletion.
Junction primary keys already support note-to-tag traversal; extra
`(subject, tag_id, note_id)` indexes support reverse tag-to-note traversal and
cascading tag deletion for both paths. Indexes do not change generated types
or the schema fingerprint. This migration uses transactional `CREATE INDEX`,
which can block writes while building indexes on populated tables; plan its
application window accordingly.

Application schemas need indexes matching their RLS predicates, filters and
ordering; foreign keys do not automatically create indexes on referencing
columns. Keep statistics current and inspect `EXPLAIN (ANALYZE, BUFFERS)` as the
restricted API role with representative JWT claims. Running as the table owner
can bypass RLS and produce misleading evidence. The local acceptance fixture
checks 30,000 notes across 200 owners, comments, and both junction paths. It
verifies ordered index scans for ordinary pagination and indexed joins. A
selective filtered cursor may use a bitmap index scan plus a sort bounded to
the owner's matching rows. These checks run without
disabling sequential scans or imposing machine-specific timing thresholds.
These are PostgreSQL query-plan checks, not an HTTP throughput benchmark or
production latency guarantee. Exact counts still scan matching visible rows;
deep offset pages still discard earlier rows. Use cursor pages for continuation.

Pass `.abortSignal(signal)` to typed reads, for example
`notes.cursorPage().abortSignal(AbortSignal.timeout(5000)).retry(false)`.
The runnable example uses a five-second deadline. Default PostgREST client
handling returns `{ data: null, error, status: 0 }` for an aborted read or a
network failure; inspect `signal.aborted` to distinguish cancellation. With
`.throwOnError()`, transport failures throw their original error and database
errors throw `PostgrestError` with the server's code. The proxy returns HTTP 504
with `query_timeout` for its upstream timeout, HTTP 503 with
`data_api_unavailable` when the engine is unavailable, and HTTP 401 with
`token_invalid` for invalid or expired sessions.

Connect the starter's renewal helper to your identity provider:

```ts
const client = notesClient({
  url, subject,
  accessToken: () => identityProvider.getAccessToken(),
})
const result = await readCursorPageWithSession({
  client,
  signal: AbortSignal.timeout(5000),
  renewSession: signal => identityProvider.renewSession({ signal }),
})
```

Import `readCursorPageWithSession` from `./session.js`. It resolves the token
again on each read and retries once after a 401. It disables the underlying
client's automatic retries for those reads and never renews in a loop. A
renewal callback receives the same abort signal; your identity provider should
honor it. A renewal failure throws to the caller; handle it with a generic session error.
The helper is for reads only. Do not automatically replay mutations after a
network failure or cancellation: the database may already have committed.
Client cancellation closes the proxy's upstream connection but does not
promise database rollback or immediate SQL cancellation. Log status and stable
codes rather than full error objects, SQL details, request headers or tokens;
database errors can contain application row values.

The starter supports typed bulk writes:

```ts
const inserted = await notes.createMany([
  { body: 'first' },
  { body: 'second', priority: 3 },
])
if (inserted.error) throw new Error('Batch insert failed')
const saved = await notes.saveDetails(inserted.data.map(note => ({
  note_id: note.id, summary: 'initial summary',
})))
```

`createMany()` binds every row to the configured subject and uses
`defaultToNull: false` so omitted fields use database defaults, even when another
row supplies that field. Returned rows include generated ids and timestamps.
`saveDetails()` upserts using the `(subject, note_id)` primary key and returns
inserted or updated rows. Both helpers derive input types from the generated contract, reject empty
batches locally, and disable automatic retries for writes.
Conflict targets must match a primary key or unique constraint: strings passed
to `onConflict` are validated by PostgreSQL, not TypeScript. Duplicate inserts
return `23505`; an invalid target returns `42P10`; repeated keys in one updating
upsert return `21000`. `ignoreDuplicates: true` skips conflicts and returns only
rows actually inserted when combined with `.select()`.

A batch mutation is one database transaction. RLS or constraint failure in any
row rolls back the whole request, including earlier updates in that upsert.
Separate API requests are separate transactions: a failed later request does
not undo a successful earlier insert. The example above is therefore two
transactions. RPC can provide one transaction per function call; transactions cannot span HTTP requests.
Use an application-owned transaction on a trusted backend when multiple writes
must commit together. Avoid automatic retries after ambiguous network failures.
The existing request-body and response-row caps apply to bulk writes; the number
of returned rows does not prove the number committed for oversized batches.
Keep batches small enough to verify every returned id before relying on the
result for follow-up writes or cleanup.

Notes now expose a database-managed integer `version`, initially 1. Save a note
with `notes.update(note.id, note.version, { body: 'edited' })`. The helper filters
by id and expected version in one atomic PATCH and disables automatic retries.
Exactly one of two concurrent writes using the same version can succeed. A
successful update returns the incremented version. A stale version, deleted
note or RLS-hidden note returns a local result with status 409 and
`error.code === 'update_conflict'`; this is a client conflict result, not an HTTP
409 returned by PostgREST. Reload before deciding whether to reapply the edit.

The database trigger increments versions on every update, including raw PATCH
and bulk updates, and rejects attempts to change the version directly. The
starter helper requires an expected version; raw `db.from('notes').update(...)`
queries must add their own `.eq('version', expected)` filter. The database does
not inspect REST filters to enforce that requirement. A raw filtered bulk PATCH
updates only matching rows; stale rows are skipped rather than causing the
whole batch to fail. Check returned ids and versions if every row must match.
Use a trusted backend transaction for all-or-nothing checks across different
row versions. `saveDetails()` concerns a separate table and does not version
notes or offer concurrency checks for detail summaries. Versioning protects
note updates through the helper; it does not turn separate requests into one
transaction or protect unguarded deletes. Existing notes receive version 1 when
the migration is applied; regenerate types and update callers for the new
update signature before adopting this starter revision.

Schema changes need a compatibility rollout as well as regenerated types.
The local acceptance suite keeps an old compiled starter client alive while
migrating its disposable database and restarting PostgREST after each revision.
It verifies that nullable additions and required columns with defaults preserve
old reads and writes, while removing a required field's default breaks old
inserts. Renaming or dropping projected columns breaks old reads and writes.
Changing an integer column to text can return valid JSON with strings where an
old TypeScript client expects numbers; generated types provide no runtime
validation. Regenerating the contract makes those incompatible client sources
fail compilation.

The fingerprint describes the exported type contract, not every database
behavior. Changing a default from 0 to 5 leaves the fingerprint unchanged because
both columns still have defaults and the same type, yet new inserts behave
differently. A successful `types --check` is not proof of migration compatibility.
Review defaults and other database behavior alongside the generated contract.

For a rename such as `body` to `title`, use an expand-and-contract rollout:

1. Append a migration adding nullable `title` while retaining `body`. Preserve
   RLS, ownership-bound foreign keys and the old grants. Backfill existing values
   and keep old writes synchronized into `title`, using a reviewed compatibility
   trigger or trusted backend. With note versioning, backfills that update notes
   advance their versions; clients may need to reload.
2. Refresh and wait for readiness, then generate types and test both old and new
   clients. Deploy new clients that read `title` with a fallback to `body` and
   write both fields while the old `body` requirement remains. Resolve conflicting
   dual writes explicitly. Keep the compatibility path for older clients and
   rollback deployments.
3. Verify old clients and deployments have retired before appending a migration
   that removes `body` or tightens the new requirement. Refresh, regenerate and
   test again. The old client can no longer be used for application rollback
   after contraction; prepare a schema-compatible rollback plan first.

Do not rename a column in place while old clients still need it. Database
migration, runtime refresh and client deployment are separate operations with
separate failure boundaries; `data-api sync` does not make them one atomic
release. These compatibility tests use local migrations and real engine
restarts; live staging rollout qualification is still required.

### Browser example and CORS

Install the starter client and SDK as described above, then run:

```sh
npm run build:browser --prefix client
npm run serve:browser --prefix client
```

Open `http://127.0.0.1:3030`. Configure that exact origin in the Data API's
`--origins` setting and redeploy before testing. Enter the API URL, the signed-in
application user's subject and application JWT. Management credentials must
never enter the browser. The example clears the token field immediately and
keeps the token only in memory for the request; it uses no persistent storage
or external scripts. In an application, provide the identity provider's current
session token through the SDK's `accessToken` callback.

The read uses a five-second abort signal, disables retries and displays the
RLS-filtered rows and exact count. Allowed origins can read `Content-Range` and
`Preference-Applied`; an expired JWT returns a readable 401. A blocked origin
produces status 0, which browsers also use for network failures. CORS governs
browser access; JWT verification and database RLS enforce authorization.
`/healthz` requires no application token and returns no application data.

Run `make data-api-browser-acceptance` with `DATA_API_CHROMIUM_BIN` pointing to
Chromium, plus the disposable PostgreSQL URL and pinned PostgREST executable
required by the acceptance suite. This gate requires a real browser; CI runs
it against separate allowed and denied origins, including preflights, exposed
headers, expired sessions and cookie omission. Live staging remains pending.

### Atomic function calls (RPC)

Opt in an owner-reviewed PostgreSQL function with an exact comment:

```sql
COMMENT ON FUNCTION api.create_note_with_tags(text, text[]) IS '@gregale:rpc';
REVOKE ALL ON FUNCTION api.create_note_with_tags(text, text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION api.create_note_with_tags(text, text[]) TO your_data_api_login;
```

Use the binding login role, not an application user's subject, for the grant.
The starter's append-only `0008_note_rpc.sql` creates this security-invoker
function and revokes public execution. Grant execution as the migration owner
before refreshing and exporting types. The function takes `note_body` and
optional `tag_names`, derives ownership from the verified JWT subject, and
creates the note, tags and attachments in one transaction. A constraint or RLS
failure rolls back every statement.

```ts
const result = await db.rpc('create_note_with_tags', {
  note_body: 'Atomic note', tag_names: ['work', 'todo']
}).retry(false)
if (result.error) throw result.error
// result.data is the generated notes Row[]
```

Only POST RPC is exposed. The same application JWT, fixed binding SQL role,
body/query limits and database policies apply. No management key or caller-
supplied ownership role is accepted. The starter also exposes
`notesClient(...).createWithTags(body, tags)` with retries disabled. Do not
replay mutations after an uncertain network outcome without application-level
idempotency. Abort and token callbacks work as they do for CRUD.

Type export and fresh startup include only annotated, executable security-
invoker functions in exposed schemas. Supported signatures have unique names
without overloads, named input arguments of recognized types, and scalar, void,
scalar-set or exposed-relation-set results. Defaults make arguments optional;
PostgreSQL permits null arguments and scalar results. Variadic, unnamed,
OUT/INOUT, arbitrary composite and non-set composite signatures are excluded.
Unapproved functions are absent from generated `Functions` and public OpenAPI,
and their RPC routes return 404. OpenAPI advertises approved calls as POST only.

Function bodies remain owner-authored application code: invoker status alone
cannot prove a body safe. Schema-qualify objects, review all called functions,
and apply RLS to every user-specific table. Refresh with a **fresh restart**
after changing signatures, annotations, execution grants or security modes,
then regenerate types and check callers. Startup captures the gateway allowlist;
exporting types or sending PostgREST SIGUSR1 alone does not replace it. Revoking
SQL execution takes effect immediately; removing an annotation alone requires
the fresh restart. Database changes are not undone by a failed app release.

### Idempotent note creation

Apply the append-only `0009_note_idempotency.sql` migration, grant the binding
login execution on both functions, then fresh-restart the Data API and regenerate
types:

```sql
GRANT EXECUTE ON FUNCTION api.create_note_with_tags(text, text[]) TO your_data_api_login;
GRANT EXECUTE ON FUNCTION api.create_note_with_tags_once(uuid, text, text[]) TO your_data_api_login;
```

The existing `api` table grants apply to `api.note_create_receipts`. Its metadata
appears in the generated schema, but RLS denies ordinary REST reads and writes.
Receipt access requires the function's transaction-local scope and the verified
JWT subject. The function restores that scope on success and failure. This guard
protects the HTTP API; it does not restrict an owner-authored function or a SQL
session that can explicitly set the same PostgreSQL parameter. Review functions
before opting them in, as with other RPC calls.

Choose one UUID **per logical operation**, retain it with the payload until the
outcome is resolved, and reuse both after a timeout or lost response:

```ts
const key = crypto.randomUUID() // create once; retain this key for retries
const payload = { body: 'Atomic note', tags: ['work', 'todo'] }
const result = await notesClient(session).createWithTagsOnce(key, payload.body, payload.tags)
// If the network outcome is uncertain, call again with this same key and payload.
```

The raw typed call is `db.rpc('create_note_with_tags_once', {
 idempotency_key: key, note_body: payload.body, tag_names: payload.tags
}).retry(false)`. Automatic mutation retries remain disabled; the caller decides
when to replay. PostgreSQL validates UUIDs, and the starter wrapper rejects
noncanonical UUID strings before sending a request.

Concurrent calls with the same subject and key serialize, then return the
original note snapshot without creating more rows. The same key with a changed
body or tags returns HTTP 409, code `PT409`, message `idempotency_conflict`.
Tag order matters; omitted tags and an empty array are equivalent. Keys are
scoped to the authenticated subject and this operation, so another user can use
the same UUID independently. Failed transactions save no receipt and leave the
key available for a corrected attempt.

The receipt and all note/tag writes commit together. Transaction-scoped
advisory locks serialize requests; hash collisions cause extra waiting rather
than incorrect matches because the subject/key primary key determines identity.
PostgREST uses the function's explicit read-committed isolation setting, even
when the role has a stronger default. Direct SQL callers must also use
read-committed transactions. Existing query timeouts bound lock waits.

Receipts preserve the original response even after the note is edited or
deleted; a replay does not recreate a deleted note. They retain the original
content and consume database storage. They have **no automatic expiry**. The
migration owner must manage retention and include receipts in any deletion or
privacy policy. Removing a receipt ends that key's duplicate-prevention window;
do not purge receipts while retries are still possible. Breaking changes to
note row types can make historical snapshots incompatible, so plan receipt
migration or retirement together with schema changes. A fresh key always means
a new operation; the original `createWithTags` remains non-idempotent.

### Receipt retention and owner cleanup

Apply `0010_receipt_retention.sql` to add the cleanup scan index. Choose a
retention window longer than every supported client retry/offline window, then
calculate an absolute UTC cutoff. There is no automatic expiry or default
retention duration: a receipt older than the cutoff becomes eligible, but
retries still replay it until a cleanup transaction commits its deletion.

Run the tool in an owner-controlled migration session with
`MIGRATION_DATABASE_URL` configured through the migration binding. The session
must assume the stable receipt-table owner role; the serving Data API login is
refused. Do not send migration credentials to a browser or paste them into a
command. The tool is not exposed through the application API and refuses a
receipt table configured with FORCE ROW LEVEL SECURITY.

```sh
# Preview only; no receipts are deleted.
npm run receipts:cleanup -- --before 2026-10-01T00:00:00.000Z
# Explicitly delete at most 200 receipts in two batches of 100.
npm run receipts:cleanup -- --before 2026-10-01T00:00:00.000Z --apply --batch-size 100 --max-batches 2
```

The cutoff must be an exact UTC timestamp with milliseconds and must not be in
the future. Selection uses `created_at < cutoff`, so the exact boundary is
retained. Batch size defaults to 100, maximum batches to one. The JSON summary
contains counts and options, never keys, subjects, payloads or credentials.
Dry-run counts describe one read-only snapshot; they do not reserve rows for a
later apply. Each deletion batch commits independently. If a later batch or
summary fails, earlier committed deletions remain applied; inspect and rerun
rather than assume rollback of the whole command.

Cleanup uses nonblocking row and transaction-advisory locks. Receipts used by
an active RPC or another cleanup transaction are skipped. Remaining eligible
receipts may therefore persist even when a run deletes zero rows; rerun after
contention clears. A successful purge removes only receipts. It does not delete
notes, tags or attachments. Reusing a purged key starts a new operation and can
create another note, even with the original payload. New receipts receive a new
creation time and are retained by the old cutoff.

Deleting a note through CRUD does not erase its receipt: retries can still
return the original content snapshot without recreating the note. Include
receipts in owner-managed deletion/privacy workflows. Purging a snapshot removes
that replay response and ends duplicate protection for its key. Retention
cleanup across all users is separate from deleting one user's business data;
coordinate those policies and communicate the retry window to clients.
