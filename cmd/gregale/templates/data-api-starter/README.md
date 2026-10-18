# Data API starter

Versioned PostgreSQL migrations, a typed notes client, two-user row-level
security, a migration-to-client workflow and application CI.

```sh
gregale init --template data-api-starter --path notes
cd notes
```

With an approved matching CLI/SDK bundle, new projects can instead run:

```sh
gregale data-api onboard --path notes --bundle /path/to/data-api-bundle.json
```

Run this from outside an existing project. It creates the starter, verifies and
installs the SDK, generates types, checks the client and replays the scenarios.
It removes disposable services when done; run `gregale data-api dev --check`
inside the new project to start a development session. See the
[first-application journey](https://gregale.dev/docs/data-api-onboarding).

For local development, install Node.js 22+, npm and Docker, then run
`gregale data-api dev`. It starts a disposable database and authenticated API,
applies these migrations, generates client types, and verifies Alice/Bob RLS
isolation. The printed private `.gregale/data-api-dev-*.json` file contains
the API URL and test tokens. Ctrl-C removes the database and identities;
generated types remain. New numbered migrations are applied automatically while
the command runs; the API URL, test tokens and local data survive reloads.
Restore edited or deleted applied migrations and add a new migration instead.
Use `--watch=false` to disable watching. After installing
the pinned SDK and client dependencies below, add `--check` to run client
typechecks and tests, or `--port 0 --check --once` for a local verification run.
See [local development](https://gregale.dev/docs/data-api#develop-locally) for
options and cleanup details.

The starter includes a ready-to-run `data-api.requests.json` collection:

```sh
gregale data-api dev --port 0 --once --replay data-api.requests.json
# Run just the ownership checks:
gregale data-api dev --port 0 --once --replay data-api.requests.json --scenario notes-rls
```

The `notes-crud` scenario creates, reads, updates and deletes a note, including its database-managed version. `notes-rls` verifies Bob cannot read, update or delete Alice's captured note ID, and Alice cannot insert a note owned by Bob. `notes-rpc` calls `create_note_with_tags`, checks ownership and the attached tag, then deletes its note and tag. Each scenario captures generated IDs and checks its cleanup, so it can be replayed repeatedly in the inspector's **Replay all scenarios** view. These local checks need Node.js 22+, npm and Docker; they do not require a Gregale account, staging database, or pinned SDK artifacts.


Reloads report schema compatibility before regenerating types and save the last
accepted contract to `.gregale/data-api-contract.json`. Use `--check-breaking` to
reject breaking changes against that existing contract. For a fixed CI baseline,
copy it to `schema.json`, commit that file, and run
`gregale data-api dev --once --check-breaking --baseline schema.json`.
An explicitly selected baseline is never overwritten. Rejected changes preserve
the contract and types; repair committed SQL with a new compatible migration.

The root deploys an ordinary migration app. Its release task applies SQL before
activation; its serving process only answers `/healthz` and never opens a
database connection. The generated Data API is a separate app. The client,
owner tools and CI files are excluded from the migration app's upload.

The local dev server prints a private **Inspector** link. Open it to browse table columns, relationships and RPCs, switch Alice/Bob test identities, and send authenticated requests. The inspector shows compatibility changes and migration errors even while the API is paused; successful reloads refresh its schema. Keep the link private: it grants access to local test identities. Restarting development invalidates it.

Use **Saved requests** to save the current method, path, JSON body, and Alice/Bob identity in `data-api.requests.json` at the project root. Set an expected HTTP status and optionally an exact JSON response (object key order is ignored). Load or delete individual requests, replay one, or replay all sequentially; each replay reports passing and failing assertions. Replay executes writes as saved, so POST, PATCH, DELETE and RPC calls can change local data. Session tokens and authentication headers are never saved. The collection survives dev restarts and can be committed for teammates; use test data in request bodies. Saved requests are excluded from newly initialized migration app deployments. Conflicting edits require reloading the collection before saving. Run the same scenarios from the CLI or CI with:

```sh
gregale data-api dev --port 0 --once --replay data-api.requests.json
# Machine-readable JSON lines:
gregale --json data-api dev --port 0 --once --replay data-api.requests.json
```

`--replay` requires `--once`; its path is relative to `--directory`. The command validates the collection before starting Docker services, verifies RLS, then executes every saved request in order against the disposable API using fresh Alice/Bob tokens. It prints each assertion result and a summary, exits nonzero if any request fails or replay is interrupted, and removes services on either outcome. JSON replay records use `event: "replay_result"` and `event: "replay_summary"`; they contain assertion metadata rather than response bodies or session tokens. An empty collection reports zero checks. Combine `--check` or `--check-breaking` with replay for client and schema checks; these run before replay. Request failures do not stop later scenarios, so use independent checks or account for earlier writes.

Saved requests can capture scalar response values with JSON Pointers and use them in later steps:

```json
{
  "version": 1,
  "requests": [
    {
      "name": "Alice creates a note",
      "method": "POST",
      "path": "/rest/v1/notes",
      "identity": "alice",
      "body": { "subject": "alice", "body": "Workflow note" },
      "expect": { "status": 201 },
      "capture": { "note_id": "/0/id" }
    },
    {
      "name": "Bob cannot read that note",
      "method": "GET",
      "path": "/rest/v1/notes?id=eq.{{note_id}}",
      "identity": "bob",
      "expect": { "status": 200, "json": [] }
    }
  ]
}
```

Use the inspector's **Response captures** field to edit the same map. Selectors use JSON Pointer syntax: `/0/id` reads the first row's ID, `~1` escapes a slash in a key, `~0` escapes a tilde, and an empty pointer selects the whole response. Captures accept strings (up to 4096 characters), numbers, booleans, and null; objects and arrays are rejected. Names use letters, digits and underscores, starting with a letter (maximum 64 characters).

`{{note_id}}` works in paths, JSON body values and expected JSON values. A whole JSON string placeholder preserves the captured scalar's type; embedded placeholders produce strings, and path replacements are URL encoded. Object keys, identity and HTTP status are not templated. Every replay starts with an empty variable set: use **Replay all** for dependent steps, or the CLI's ordered replay. A step publishes captures only after its status and JSON assertions pass and every selector resolves. Failed steps clear the names they declare; missing variables fail dependent steps before sending a request. Capture results show variable names, never captured values, and variables are neither saved nor shared across replays.

Organize independent workflows as version 2 named scenarios:

```json
{
  "version": 2,
  "scenarios": [
    {
      "name": "notes-rls",
      "requests": [
        {
          "name": "Bob sees no Alice notes",
          "method": "GET",
          "path": "/rest/v1/notes?subject=eq.alice",
          "identity": "bob",
          "expect": { "status": 200, "json": [] }
        }
      ]
    },
    { "name": "rpc", "requests": [] }
  ]
}
```

The inspector can create, select and delete scenarios, edit each scenario's requests, replay the selected scenario, or replay all scenarios. Creating a named scenario upgrades a version 1 collection while preserving its requests in `default`. Version 1 files still work unchanged as that implicit scenario. Keep at least one scenario; delete its requests to empty it.

```sh
gregale data-api dev --port 0 --once --replay data-api.requests.json --scenario notes-rls
```

Omit `--scenario` to run every scenario in file order. The flag requires `--replay`, and unknown names fail before Docker services start. Each scenario gets fresh variables; all scenarios share the same disposable database, so workflows should clean up their own rows or account for earlier writes. A failure in one scenario does not stop later scenarios. Scenario names are unique identifiers starting with a letter, followed by letters, digits, underscores or hyphens (maximum 64 characters). Request names must be unique within a scenario; there is an aggregate limit of 100 requests and a 64 KiB file limit.

Version 2 JSON output adds `scenario` to each `replay_result` and emits `replay_scenario_summary` with the scenario name and total/passed/failed counts. The final `replay_summary` aggregates only the selected scenarios. Version 1 JSON output remains unchanged.



## Configure local development

`data-api.dev.json` declares this starter's migrations, identities, required
`notes-rls` verification, types output and client checks. Change it to develop
another schema with named test identities and optional seed SQL. Required
scenarios run before readiness and after migrations, and must exercise two
subjects. Restart dev after changing configuration or seeds. `--config FILE`
selects another config and `--output FILE` overrides its types destination.
The inspector uses the configured identities. Legacy projects without a config
retain the original Alice/Bob notes checks.

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

Ordinary views that directly project complete foreign keys or referenced keys
also receive typed relationships, including renamed columns and readable view
chains in the same exposed schema. Use `security_invoker=true` and keep RLS on
the underlying tables. Expressions, missing key columns, unions, inaccessible
origins and materialized views are excluded. Use constraint hints for multiple
foreign keys; repeated aliases of one composite key can remain ambiguous even
with a hint. Changing key origins changes the fingerprint and is checked by
`data-api diff`, including when the view's column types stay unchanged.
`comments.note_id` is nullable; unattached comments have a null parent embed.
An absent details row yields null, and notes without replies return an empty
array. Add `!inner` to an embed to omit rows without a visible match. Nested
projection types only expose the selected columns.

Approved relation-set RPCs also support typed nested projections and filters.
A uniquely named `@gregale:rpc` STABLE/IMMUTABLE security-invoker function with
one named, required source-row argument and a SETOF readable relation result
defines a computed relationship in the same exposed schema. Source fields must
use recognized types. `ROWS 1` embeds an object or null; direct RPC responses
remain arrays. Names cannot shadow relations, FKs or source columns. The owner
permission workflow grants supported functions and blocks executable unapproved
relationships, including PUBLIC execution. Revoke those grants or make the
function eligible, then fresh-restart and regenerate contracts. Snapshot version
3 carries this metadata; upgrade the CLI and owner dashboard with the runtime.
Function bodies still need owner review and subject RLS; supplying a row as an
RPC argument does not prove the caller owns that row.

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
  client tests on pull requests and main pushes, then replays the shipped CRUD,
  RLS and RPC scenarios against a disposable local API. Scenario and request
  names appear in the job logs; failed assertions fail the job. It restores the
  verified CLI and locked SDK from the committed bundle pin and needs no Gregale
  account credential or application JWT.
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
and client CI need the pinned bundle's HTTPS base URL to restore the CLI. Pin
a CLI build that supports `data-api dev --replay` and version 2 scenarios. Use
a publicly readable artifact source for pull-request client CI. The owner key is passed
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

Open `http://127.0.0.1:3030`. Local `data-api dev` already allows this
loopback origin. For a hosted runtime, configure that exact origin in the Data API's
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
scalar-set, named-composite or exposed-relation-set results. Named composites
return objects; `SETOF` composites return arrays. Composite arguments accept JSON
objects. Defaults make arguments optional;
PostgreSQL permits null arguments and scalar results. Eligible computed
relationships may accept their source relation row. Variadic, unnamed,
OUT/INOUT, unsupported or inaccessible composite dependencies, and non-set
relation-row signatures are excluded.
Unapproved functions are absent from generated `Functions` and public OpenAPI,
and their RPC routes return 404. OpenAPI advertises approved calls as POST only.

Named `CREATE TYPE api.address AS (...)` objects generate
`Database['api']['CompositeTypes']['address']` and a
`CompositeTypes<'address'>` helper. Nested fields and arrays use these types.
Composite fields remain nullable, including domain attributes, because a NULL
composite RPC result expands to an object of null fields. All object keys are
required by the generated contract. Hidden dependencies become `unknown` and
exclude dependent RPCs. These types do not add REST table endpoints.

New exports use normalized snapshot version 3 in the version 1 contract envelope.
Upgrade the CLI before consuming them. The current diff command also reads
legacy version 1/2 baselines and flags nested composite changes even when the RPC
return type name stays unchanged. Removed fields, changed field types/nullability
and added required keys are breaking; new composite types are compatible.

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

### Preview and grant RPC permissions

The starter ships an owner-side permission tool using the **same catalog and
signature parser as the serving runtime**. In an owner-controlled migration
session, configure `MIGRATION_DATABASE_URL` through the migration binding, then
run:

```sh
npm run rpc:permissions
npm run rpc:permissions -- --apply
```

The default is a read-only preview. It reports the selected SQL role, annotated
functions, missing explicit grants and readiness blockers. A missing grant or
blocker produces exit status 1; a ready plan produces 0. `--apply` grants only
supported, owner-authored `@gregale:rpc` functions to that role, atomically in one
transaction. It does not grant table/schema access, change RLS, revoke existing
permissions or grant execution on every function in the schema. Public execute
permission does not substitute for the explicit binding-role grant managed by
this tool. Output never includes database URLs, passwords, JWTs or function
bodies; role names and function names are owner metadata.

Automatic discovery uses matching Gregale migration/data_api credential scope
markers and requires exactly one active login. During a rotation overlap,
discovery refuses to guess and reports candidate names. Select the intended
serving binding role explicitly, or wait for the old role to retire:

```sh
npm run rpc:permissions -- --role your_data_api_login
npm run rpc:permissions -- --role your_data_api_login --apply
```

A retired NOLOGIN role, different credential scope, elevated role, membership,
object ownership, schema creation, outside-schema table access or executable
security-definer function is refused. The selected role must have the existing
Data API statement-timeout configuration, connection/schema usage, table and
sequence grants, and future-object default grants. Tables must have RLS enabled
and policies configured; views must be security-invoker views. Materialized and
foreign tables are blocked by this conservative readiness check. Unsupported,
overloaded or foreign-owned annotated functions block apply rather than receive
execution grants. The tool assumes the session's `current_user` owns `api`;
an unmarked migration session requires an explicit role selection.

Readiness here means ACL and catalog checks passed. It does not prove policy
correctness, transitive function dependencies or actual two-user isolation;
review function bodies and run application acceptance tests. Apply shares the
migration runner's transaction advisory lock. Coordinate direct owner DDL with
that workflow; a preview is not a reservation of catalog state. After grants,
fresh-restart the Data API and regenerate types before compiling callers.
Re-run for the replacement role after credential rotation. The tool does not
publish, deploy, rotate credentials, or revoke the old role.

The shared catalog files are included automatically in Gregale's scaffold and
template archive under `migrations/rpc-runtime/`. Source-checkout acceptance
copies the same canonical files into its temporary starter; do not maintain a
separate eligibility implementation.

### Permission setup during sync

The manifest and Procfile now use `node migrations/release.mjs`. This release
commits migrations, applies eligible opted-in RPC grants, and validates role,
schema and RLS readiness. The existing `data-api sync` migration command runs
`deploy --wait`, so a permission failure stops sync before runtime refresh,
type export or client checks. Credentials stay in the remote migration binding.
Committed migrations remain if permission setup fails; fix the reported blocker
and rerun. `npm run migrate` continues to run migrations only.

During overlapping credential rotation, configure the owner-controlled release
command as `node migrations/release.mjs --role your_data_api_login`, or wait for
the old login to retire. Existing projects must include this tooling and update
their release command to adopt the integration.

Custom sync configs may include an optional `permissions` command, executed after
`migrate` and before refresh. It shares the workflow deadline, directory/argv
validation and cancellation handling. Omit it for this starter because its
remote release already handles permissions. See the
[workflow documentation](https://gregale.dev/docs/data-api#rpc-permissions-in-the-sync-workflow)
for an example; keep credentials outside config files and command arguments.

### Application token for serving-contract verification

Before `gregale data-api sync`, configure an application JWT through
`GREGALE_DATA_API_ACCESS_TOKEN` in your environment or CI secret injection. Use
your configured identity provider; an account API key is not a valid substitute.
The token must remain valid until sync finishes. To use a different variable,
set `"access_token_env": "YOUR_APPLICATION_JWT"` in `data-api.json`; never put
the token value in the config or command arguments.

Sync compares the authenticated `/__gregale/schema` startup fingerprint with
private type export before replacing `client/src/database.types.ts` or running
type checks. A stale or unavailable serving contract stops the workflow and
preserves existing types. It requires the current Data API runtime; older
runtimes return 404. The fingerprint covers the generated contract, including
RPC grant eligibility, and does not prove RLS semantics or function behavior.
A successful JSON sync receipt includes `contract_verified: true`.

### Correlate failed requests

Capture the Data API's `X-Request-Id` response header when diagnosing a failure,
then find that ID in the API app's JSON request logs. The header is exposed to
allowed browser origins; gateway problem bodies also include `request_id`.
Engine response bodies remain unchanged. The gateway generates IDs itself and
ignores caller IDs. Its logs use fixed route categories and exclude tokens,
subjects, query values, table/function names, credentials and application bodies.

### Capture diagnostics from the typed client

`notesClient` and `readBrowserNotes` accept the SDK's optional `onResponse`
callback. It receives only `{ requestId, status, durationMs }`; use the ID to
find the matching API request log. The SDK emits no logs by default. Missing,
malformed or browser-inaccessible IDs are null. Callback failures do not affect
data access, and asynchronous callbacks are not awaited. Each HTTP response
produces a callback, including errors and retry attempts; network failures
without a response do not. Duration measures token lookup and fetch through
headers, not body processing, SQL execution or platform wake time. See the
[Data API guide](https://gregale.dev/docs/data-api#typed-client-response-diagnostics)
for a typed example.

### Check committed types in local CI

Run `gregale data-api dev --port 0 --once --check-types --check` after installing
client dependencies. The disposable database applies your project migrations,
verifies authorization scenarios, and compares generated types byte-for-byte
with the configured output file. Missing or stale types exit nonzero without
creating or overwriting that file. `--check` then runs configured client checks;
`--check-types` requires `--once`. Containers and volumes are removed on success
and failure. No hosted account or staging credentials are required.

To update types, run `gregale data-api dev --port 0 --once` with the same
`--directory`, `--config` and `--output` options, then commit the generated file.
This detects drift; use `--check-breaking` separately for compatibility checks.
The workflow still creates local contract artifacts and runs configured setup
and authorization steps; the types output alone is preserved by this flag.

The starter's `notesClient.cursorPage()` uses the SDK's `cursorPage` and
`cursorFrom` helpers. Its public `{ created_at, id }` cursor format and strict
timestamp/id validation remain the same, including PostgreSQL microseconds.
Upgrade the installed SDK bundle alongside the starter to use these helpers.

### Validated RPC form

The browser example also includes a note-and-tags form backed by the approved
`create_note_with_tags_once_v2` function. Apply migrations and RPC permissions,
refresh the serving schema and install a current paired SDK bundle before using
it. V2 requires `0012_note_replay_expiry.sql` and execution permission on
`read_note_create_receipt_v2(uuid,text,text[])`. Both RPCs are granted by the
normal owner permission workflow. The controller explicitly selects
`replayContract: 'v2'`; existing callers retain v1 unless they opt in.

Enter the session information above, then a body and optional comma-separated
tags. The application-owned parser trims content, rejects a blank body or empty
tag names and supplies an empty tag list by default. Inline field messages appear
before token lookup or transport. `client/src/note-form.ts` connects this parser
to `validatedRpc()`, and preserves the typed `id,body,priority,version` result.
Constraint, authentication and permission failures use curated messages; raw
SQL details and parser exceptions are never rendered. Text is rendered with
`textContent`, including saved note content.

Requests use `dataRequest()` with a five-second deadline and automatic retries
disabled. If a save cannot be confirmed, the controller retains a frozen request
key and payload in memory and blocks new submissions. The API URL, subject and
note fields stay locked; supply a current token for the **same signed-in user**
and choose **Check pending save**. This reads the receipt using the original key
and payload, then uses `reconcileMutation` to confirm the saved snapshot under
the controller's captured subject. It never replays the write. Missing, purged,
hidden or uncommitted receipts stay unresolved, as do lookup failures and payload
conflicts; all preserve the original pending operation. The subject input does
not override JWT ownership; production session management must keep API and
identity context stable when renewing credentials.

**Retry original save** is a separate explicit action with replay-window review.
It calls the idempotent creation function with the same frozen key and payload.
An unsuccessful retry cannot discard the original uncertain operation, including
when the latest token is refused. The controller exposes these actions separately
as `reconcile()` and `retry()`.

Resolve pending saves promptly. V2 UUIDv7 keys expire 24 hours after their embedded
timestamp, with five minutes of future clock tolerance. The database rejects an
expired write even after its receipt is purged. The lookup can still confirm a
retained receipt after expiry; an expired missing receipt stays unresolved.
Keep the original operation for business review; expiry never proves rollback.
Legacy v1 replay after receipt deletion can still create another note.
The example does not
persist tokens or pending requests, and closing or reloading the page loses the
recovery state. A production app should retain operation state under its own
identity and privacy policy, including the original API and contract version.
The token input is cleared immediately and the in-memory token is cleared after
each action; paste a fresh application token for each save or recovery action.

Starter client tests cover field feedback, request/result types, safe messages,
concurrent-action suppression, deadlines and immutable recovery payloads. The
PostgreSQL/PostgREST acceptance suite tests committed-response loss and retained
receipts. Required Chromium acceptance exercises the actual form, checks invalid
input sends no request, verifies token clearing and locked fields, and confirms
explicit recovery produces one saved note.

### Recoverable updates and deletes

Apply `0013_note_mutation_receipts.sql`, run the owner RPC permission workflow,
refresh the Data API and regenerate types. Install the paired SDK bundle. The
client exposes opt-in `updateOnceV2(key,id,expectedVersion,patch)` and
`removeOnceV2(key,id,expectedVersion)`, plus read-only `readUpdateReceiptV2` and
`readDeleteReceiptV2` with the same arguments. Existing REST methods and the
browser editor keep their prior behavior.

Capture a `createReplayKeyV2()` key with the original API, subject, action, target,
version and complete payload before sending. Use `classifyVersionedMutation` for
the completed write response and `reconcileMutation` for a separate receipt
lookup. Successful update receipts preserve the post-update version; delete
receipts preserve the deleted row's version. Missing, stale and hidden rows
share the same version conflict. A missing receipt, conflict or expired key
cannot itself confirm an earlier uncertain request; keep its original operation.

For owner cleanup select `--contract v2 --receipt-kind mutation`. Preview with a
past UTC cutoff before applying bounded batches. Only expired snapshots can be
purged; cleanup never deletes notes or reopens an expired key. The default
creation-receipt cleanup remains unchanged. See
[the Data API guide](https://gregale.dev/docs/data-api#versioned-update-and-delete-recovery)
for typed examples, exact errors and recovery limits.

### Atomic note updates and deletes

After migration `0014_note_atomic_batches.sql`, apply owner RPC grants, refresh
the runtime, regenerate types and install the paired SDK. Use `prepareNoteBatch`
from `client/src/note-batch.ts` to capture 1–100 distinct note updates/deletes,
then `applyBatchOnceV2(pending.key,pending.operations)`. Updates require an
expected version and nonempty body/priority patch; deletes require an expected
version and omit the patch. Any failing item rolls back the whole transaction.

Use `classifyVersionedMutation(response,noteBatchMatches(pending))` for its single
ordered acknowledgement envelope. After an uncertain response, explicitly call
`readBatchReceiptV2` with the original key/array and use `reconcileMutation` with
the same matcher. Retain the original API/identity/contract and immutable pending
operation; preparation does not persist it. Receipt snapshots can confirm the
original batch after later changes, but do not describe current rows. Never
replace a key/version automatically after missing, conflicting or expired results.

The v2 24-hour key deadline applies; expired writers stay refused after cleanup.
Saved results are capped at 1 MiB, with full rollback on overflow. Owner cleanup
uses `--contract v2 --receipt-kind batch`. See
[the Data API guide](https://gregale.dev/docs/data-api#atomic-note-batch-recovery)
for setup, examples, errors and recovery assumptions.

### Schema-driven form feedback

Run `gregale data-api dev --validators --check` to regenerate runtime insert/update
parsers alongside types, including after watched migrations. The note form uses
those generated rules before creating a request key or sending a save. Database
constraints, RPC business rules and RLS remain authoritative. Plain type generation
keeps the existing form validation. Keep your generation mode consistent in CI:

```sh
gregale data-api dev --validators --check-types --once
```

### Validate a CSV before importing notes

After generating with `--validators`, use `previewNotesCsv` from
`client/src/note-import.ts`. Supply the signed-in subject as application context;
CSV ownership columns are ignored. Map `body` and optional `priority` headers, review
all rows, and explicitly approve before submitting a successful `preview.rows`.
An invalid preview has complete counts, capped row/field issues and no partial rows.
It never retrieves a token or sends a request. CSV is limited to 1 MiB/1,000 records,
and errors use record numbers including the header. SQL and RLS recheck submissions.

### Schema-driven create and edit forms

With `dev --validators`, the browser example builds note body and edit-priority
controls from generated `formMetadata`. Labels and the editable-field allowlist
remain application-owned; subject, identity and version are never auto-rendered.
Supported limits and enum choices refresh with the generated module. Field parsers
provide feedback before transport. Read a current note ID/version before editing;
blank edit fields keep existing values, conflicts require a fresh read, and uncertain
saves are never automatically replayed. Plain types retain default starter controls.

### Enable version-checked inspector edits

Review the existing note version trigger, then copy
`migrations/examples/enable-versioned-edits.sql` into a new numbered migration.
Its `@gregale:version` column annotation declares that the database advances the
version on every successful update. The local inspector then offers reviewed edits
for visible keyed rows, with stale-version rejection and a fresh-read requirement.
Do not modify an already applied numbered migration to add the annotation.

### Owner receipt storage diagnostics

After installing dependencies, run `npm run --silent receipts:diagnostics` with
the secret-managed stable-owner `MIGRATION_DATABASE_URL`. It reports all three
v2 receipt contracts; select one with `--receipt-kind create`, `mutation` or
`batch`. The command returns one versioned JSON object with total/active/expired
counts, oldest expired deadline and age, and table/index/total physical bytes.
Counts, ages in milliseconds and sizes are decimal strings; an empty backlog has
null oldest deadline and age. No receipt payloads, keys or identities appear.

The report uses read-only transactions. Serving logins and all non-owners are
refused. Missing/FORCE RLS tables, incompatible shapes, lock contention and query
failures produce unavailable entries without measurements and a nonzero exit.
Statements and connections have 5-second deadlines, lock waits a 1-second limit
and client queries a 6-second fallback. Exact counts can time out on large tables.
Keep these cross-user aggregates in operator monitoring. Physical allocation
includes TOAST and free space and can remain unchanged after deletion. Cleanup
remains explicit. See the receipt storage diagnostics section of
https://gregale.dev/docs/data-api for output fields and failure reasons.

### Incremental note synchronization

Migration `0015_note_change_feed.sql` records subject-scoped insert/update/delete
metadata in the same transaction as note writes. Apply it, approve the two new
RPC grants through the owner workflow, refresh and regenerate client types.
`notesClient.startSync()` gives a checkpoint; `syncRows({ afterID, size })` scans
current notes by ascending id. Take the checkpoint before the scan, then use
`readChanges(cursor, size)` to catch up. Validate responses with
`parseNoteSyncCheckpoint` and `parseNoteChangePage` from `note-changes.ts`.

Re-fetch every changed id under current RLS, including deletion events. Upsert
present rows, remove missing ids, and save the next cursor only after applying
the whole page. Continue while `has_more` is true, then poll explicitly. Metadata
contains only sequence, operation, note id and version; it stores no old bodies.
Comments/tags are outside this notes-only feed. Pages can split a SQL transaction.

The latest 10,000 events per subject are retained. An expired cursor returns
HTTP 410 / `PT410` / `note_change_cursor_expired`; `noteChangeCursorExpired`
recognizes this exact result. Restart a fresh staging scan on expiry. Bind caches
to API/subject, and reset staging on identity changes. Wrong-context/future
cursors fail with HTTP 400. Missing/inconsistent history fails with HTTP 503.
Pages hold at most 100 events and cursors at most 128 bytes. Cursors are opaque
continuations, not authorization tokens.

Transactional counter locks serialize writes within one subject until commit;
other subjects have independent streams. SQL lock/deadlock failures still roll
back normally. Both polling RPCs are read-only, and direct metadata-table CRUD is
blocked. Watermarks survive deletion of all notes; stream storage grows with the
number of subjects with history. Owner history resets need an epoch strategy.
See https://gregale.dev/docs/data-api for the bootstrap/catch-up example.

### Ranked note search

Migration `0016_note_search.sql` adds `search_notes` and backfills an internal
subject/lexeme index. Apply it, approve the RPC through the owner permission
workflow, refresh the runtime and regenerate types. Use
`notes.search('running errands', { size: 20 })`, then validate the single response
page with `parseNoteSearchPage` from `note-search.ts`. Continue with the same
query and `{ after: page.next_cursor, size: 20 }` while `page.has_more` is true.

Matching uses English stemming and AND words, with stop words removed. Boolean,
phrase, prefix and fuzzy syntax are unsupported. Empty/stop-word-only queries
fail. Results contain current note fields and integer relevance, ordered by rank
then descending id. Queries allow 1,024 UTF-8 bytes and 16 distinct normalized
terms; pages 1–100 results; cursors 128 ASCII bytes. Invalid query/size/cursor
arguments return HTTP 400. Cursors bind to exact query text and verified subject.
Keep API and identity consistent; edited relevance can move rows between pages.

Ordinary writes maintain postings atomically. The search RPC runs read-only with
invoker rights, rechecks current notes through RLS, and preserves cancellation.
Direct index reads are hidden and writes rejected. Common searches can still
rank many matches within existing runtime budgets. Index storage/write overhead
scales with note lexemes. Owner TRUNCATE, disabled triggers and dictionary changes
need a reviewed rebuild. See https://gregale.dev/docs/data-api for usage.
