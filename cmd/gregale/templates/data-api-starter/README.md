# Data API starter

Versioned PostgreSQL migrations, a typed notes client, two-user row-level
security, a migration-to-client workflow and application CI.

```sh
gregale init --template data-api-starter --path notes
cd notes
```

The root deploys an ordinary migration app. Its release task applies SQL before
activation; its serving process only answers `/healthz` and never opens a
database connection. The generated Data API is a separate app. The client,
owner tools and CI files are excluded from the migration app's upload.

## Install the client

Use Node.js 22 or newer, npm, curl and tar. Obtain an approved Data API bundle,
or build one from a committed, authorized Gregale checkout with its pinned Go
toolchain:

```sh
# In /path/to/faas (output must be a new directory outside the checkout):
node scripts/build-data-api-bundle.mjs --version v0.0.0-data-api.local \
  --out-dir /path/to/data-api-bundle
# Back in the initialized notes directory:
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json
npm run typecheck --prefix client
npm test --prefix client
```

The bundle pairs a CLI and SDK from one source commit. The installer verifies
their checksums, installs the CLI at `.gregale-tools/gregale`, installs the SDK
without lifecycle scripts, and updates the client lockfile. Commit
`data-api-artifacts.json` and `client/package-lock.json`. Use the paired CLI for
the commands below, for example `./.gregale-tools/gregale data-api sync ...`.

For automatic client CI, include the authorized SDK tarball at
`client/vendor/gregale-data.tgz` in your application repository. Alternatively,
pin an immutable HTTPS directory holding the bundle artifacts:

```sh
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json \
  --base-url https://artifacts.example.com/approved/COMMIT/
```

The base URL must have a trailing slash and no embedded credentials, query or
fragment. CI restores the exact SDK checksum and refuses a different lockfile.
Automatic pull-request CI receives no artifact credentials: vendor the SDK
when its download needs authentication. Protected preview CI can use the
optional `GREGALE_ARTIFACT_TOKEN` secret to fetch the paired CLI and SDK. Downloads
require HTTP 200, refuse redirects and never forward the artifact token to
another origin. For a local bundle copy, use
`node tools/artifacts.mjs restore --from /path/to/data-api-bundle --cli` before
`npm ci --prefix client`.

`@gregale/data` is not published to npm. Public SDK distribution remains subject
to Gregale's SDK publishing policy. This workflow builds local files and does
not publish a package. Root migration dependencies have their own lockfile.
`tools/install-sdk.mjs` still accepts a standalone authorized SDK tarball for
local use; bundle-based CI requires the committed bundle pin.

## Configure and deploy

Use a ready, qualified managed database such as `notes-db`, an account/region
enabled for Data API and release tasks, and a plan with capacity for both apps
and PostgreSQL egress. Use an isolated database: the first migration creates
`api.notes` and does not adopt an existing table. Use your application's HTTPS JWT issuer, JWKS endpoint
and audience. No identity provider or database administrator key is bundled.
Keep credentials in your environment or secret store.

Reserve the migration app and attach its release-only connection:

```sh
gregale deploy --create-only --path . --dockerfile --name notes-migrations
gregale postgres attach notes-db notes-migrations --access migration --env MIGRATION_DATABASE_URL
# Use the binding ID from the attachment receipt. Wait for state "ready".
gregale postgres bindings get <binding-id>
gregale app notes-migrations egress-ports add 5432
gregale deploy --name notes-migrations --dockerfile --wait --timeout 15m
```

Create the API on the same database, then export its actual schema and validate
the client. Substitute your issuer, JWKS URL and audience:

```sh
gregale data-api create notes-data --database notes-db \
  --issuer https://identity.example.com/ \
  --jwks-url https://identity.example.com/.well-known/jwks.json \
  --audience notes
gregale data-api sync notes-data --config data-api.json
```

If you rename the migration app, update `data-api.json`. Its migration command
deploys the current working tree and waits for release success and deployment
readiness. Sync then verifies a fresh
API restart, writes `client/src/database.types.ts` atomically and runs the client
type check. The committed types are generated from this starter's migrations;
the first sync replaces them with your live database contract.

## Use application sessions

`client/src/notes.ts` provides typed list, create, update and delete operations:

```ts
import { notesClient } from './notes.js'

const notes = notesClient({
  url: 'https://notes-data.gregale.dev',
  subject: session.user.id,
  accessToken: () => session.getAccessToken(),
})
await notes.create('Hello', 1)
const { data, error } = await notes.list()
```

The third migration adds comments and one-to-one note details. The client also
supports typed nested queries:

```ts
await notes.reply(noteID, 'A reply')
const joined = await notes.listWithReplies()
// Each row has comments: { id: number; body: string }[]
// and note_details: { summary: string } | null.
const comments = await notes.listComments()
// Each comment has notes: { id: number; body: string } | null.
```

The generator preserves composite foreign-key column order and detects
one-to-one relationships from matching primary-key/unique constraints.
`comments.note_id` is nullable; unattached comments have a null parent embed.
An absent details row yields null, and notes without replies return an empty
array. Add `!inner` to an embed to omit rows without a visible match. Nested
projection types only expose the selected columns.

Both child tables enforce their own subject RLS. Their composite foreign keys
include the same subject as the parent, so a user cannot attach a comment or
details to another user's note. Deleting a note cascades to its children. The
two-user authorization check covers these joins and attachment restrictions,
and cleans up its own unattached test comments.

The token getter runs on every request. Use the identity provider's exact `sub`
as `subject`. PostgreSQL checks the token subject on every read and write;
supplying a different subject cannot grant access to someone else's data. API
tokens require `sub`, `exp`, the configured issuer/audience, and RS256 or ES256.
Owner keys are for management and type export, never for the application client.

For the runnable example, configure `DATA_API_URL`, `DATA_API_SUBJECT` and
`DATA_API_TOKEN` in your environment, then run:

```sh
npm run example --prefix client
```

For a two-user check, configure `DATA_API_URL`, `DATA_API_USER_A_SUBJECT`,
`DATA_API_USER_B_SUBJECT`, `DATA_API_USER_A_TOKEN` and `DATA_API_USER_B_TOKEN`.
Use two distinct, unexpired application sessions in your preview environment:

```sh
npm run test:authorization --prefix client
```

This check performs both users' CRUD operations, rejects cross-user access and
cleans up only notes, tags and unattached comments whose IDs it created. Missing sessions fail the check;
they do not silently skip it.

## Evolve the schema

Add `migrations/sql/0008_description.sql` and run sync again. Numbered SQL files
are applied under a transaction and database advisory lock. The private
`gregale_migrations.applied` ledger records versions and SHA-256 checksums;
already-applied SQL cannot be edited, renamed, removed or inserted out of order.
Use plain SQL without transaction-control statements; the runner owns the
transaction, so operations requiring execution outside a transaction need a
separate migration approach. All pending migrations commit together. Failed
release tasks keep the previous deployment serving, but committed database
changes are not undone by rollback.
Use changes compatible with existing clients and serialize migration/sync jobs
for each database so refresh and export observe the same schema.

`api.notes` has RLS enabled before its transaction commits. Its policy checks
the original JWT `sub` for both visibility and writes. The managed Data API
login receives current/future table grants from the stable migration owner; the
ledger schema receives no Data API grants. Do not move the ledger into `api`.

## Application CI

`gregale init` writes two workflows into `.github/workflows`:

- `data-api-client.yml` compiles positive/negative type assertions and runs
  client tests on pull requests and main pushes. It uses the locked SDK tarball
  and needs no account credential or application JWT.
- `data-api-preview.yml` is manually dispatched from main. It runs the client
  checks, verifies live types with `data-api types --check`, and tests two-user
  authorization. It never runs migrations, sync or refresh.

Configure the `data-api-preview` GitHub environment with required reviewers and
deployment branches restricted to main. Add environment variables:

| Variable | Value |
| --- | --- |
| `GREGALE_API` | Preview management API URL |
| `DATA_API_APP` | Data API app slug, such as `notes-data` |
| `DATA_API_URL` | Public HTTPS Data API URL |
| `DATA_API_USER_A_SUBJECT`, `DATA_API_USER_B_SUBJECT` | Two distinct JWT subjects |

Add secrets `GREGALE_OWNER_TOKEN`, `DATA_API_USER_A_TOKEN` and
`DATA_API_USER_B_TOKEN` to that protected environment. If the bundle location
requires bearer authentication, also add `GREGALE_ARTIFACT_TOKEN`. Preview CI
needs the pinned bundle's HTTPS base URL to restore its CLI. The owner key is passed
only to the drift-check step; the RLS step receives application sessions. Keep
sessions current and use an isolated preview database. The CLI and SDK come
from the committed bundle pin; preview CI verifies the CLI's embedded version,
source commit and build timestamp before running its drift check.
Include `linux/amd64` in the bundle's `--targets` for GitHub's Ubuntu runner,
along with your local host target when building on another platform.

The visible `ci/` files are the embedded workflow sources; the initialized
`.github/workflows/` copies are your application workflows. See the
[Data API guide](https://gregale.dev/docs/data-api) for preview enablement,
runtime bounds and staging qualification.

Many-to-many tags use `notesClient.listWithTags()` and `listTaggedNotes()`.
The `tags!note_tags(id,name)` projection names the junction explicitly; both
embedded results are arrays. The junction primary key includes both composite
foreign keys, as PostgREST requires for many-to-many discovery. Tags and links
have their own RLS policies, and subject-bound foreign keys reject attachments
to another user's note or tag. The authorization check exercises these joins
and removes only rows created by its own run.

`note_favorite_tags` provides a second path between notes and tags. Use
`listWithFavoriteTags()` / `listFavoriteTaggedNotes()` for favorites and the
existing tag methods for ordinary tags. Unhinted `tags(id,name)` or
`notes(id,body)` embeds fail with PostgREST `PGRST201` (HTTP 300); specify the
junction, for example `tags!note_favorite_tags(id,name)`. Aliases can return both
paths in one projection. An unknown hint fails with `PGRST200`. These are runtime errors; the SDK
does not validate junction hints at compile time. The two-user
check verifies both paths, including their independent RLS policies.

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
transactions. The preview does not expose RPC-based multi-request transactions.
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

Schema evolution should preserve deployed clients until they retire. Add
nullable columns or defaults before deploying clients that use them. For
renames, keep the old column, backfill and synchronize the new one, deploy
clients that can read and write both, then remove the old column in a later
migration. Refresh the runtime and regenerate types at each step. Note
backfills increment versions, so an older edit may need to reload.

The acceptance suite verifies additive reads and writes against an unchanged
compiled client, required-field defaults, renames, removals and changed column
types. TypeScript does not validate JSON at runtime: a changed database type can
silently violate an old client's assumptions. A default-value change can also
change behavior without changing the type fingerprint. A passing
`types --check` does not establish backward compatibility. See the schema
rollout section in Gregale's `docs/data-api.md` for expand-and-contract steps
and rollback boundaries. The migration runner is append-only; never edit an
already-applied starter migration to perform a breaking change.

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
