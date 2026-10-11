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

## Develop locally

Initialize the starter and run a disposable API without a Gregale account:

```sh
gregale init --template data-api-starter --path notes
cd notes
gregale data-api dev
```

The starter also ships `data-api.requests.json` with `notes-crud`, `notes-rls`, and `notes-rpc` scenarios. Run them immediately after initialization:

```sh
gregale data-api dev --port 0 --once --replay data-api.requests.json
```

The scenarios exercise CRUD and managed note versions, Alice/Bob isolation, and the approved `create_note_with_tags` RPC. They capture generated IDs, delete the records they create, and verify cleanup, so repeated inspector replays work in the same local session. Select one with `--scenario notes-rls`, or open the inspector and use **Replay all scenarios**.

The scaffolded `.github/workflows/data-api-client.yml` restores the verified CLI and SDK bundle, runs client checks, then executes this collection on GitHub's Ubuntu runner with Docker. Request and scenario failures appear by name in the job logs and return a failing exit status. CI requires a committed artifact pin with an HTTPS base URL, a `linux/amd64` CLI archive, and a CLI build supporting scenario replay; local replay works without that pin or any Gregale account credentials.


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



Install Node.js 22 or newer, npm, and Docker with a running local daemon first.
The command installs locked runtime dependencies into a temporary directory,
starts PostgreSQL 16 and the production digest-pinned PostgREST 14.3, applies
`migrations/sql`, validates RLS and RPC grants, and generates
`client/src/database.types.ts` using the restricted API login. All published
service ports and the JWT-verifying gateway bind to loopback. Select a local
Docker context: the command uses your configured Docker daemon.

The readiness message gives the API URL and a private session file under
`.gregale/data-api-dev-*.json`. It contains locally signed Alice and Bob tokens,
their subjects, and the schema fingerprint. Use its `url` and an identity's
`token` with the normal SDK. Tokens expire after 24 hours and work only for that
running instance. Before reporting readiness, the command verifies that missing
tokens and cross-subject reads and writes are rejected. Browser CORS allows
`http://localhost:5173` and `http://127.0.0.1:5173`.

After installing the pinned SDK and starter client dependencies as described in
the starter README, run:

```sh
gregale data-api dev --check
gregale data-api dev --port 0 --check --once
```

`--check` runs client typecheck and tests; `--once` verifies and exits.
`--port 0` selects an available gateway port. `--directory` selects the
project; `--output` changes the types path relative to that project.

Long-running sessions watch `migrations/sql` by default. Add a new numbered SQL
migration and save it: the command applies it, validates RLS and RPC permissions,
restarts PostgREST to refresh its schema cache, regenerates types, and reruns
optional client checks. Reloads run one at a time and include changes saved during
a reload. The API URL, test tokens, and existing database rows stay intact.
Use `--watch=false` to disable watching. `--once` always performs a single run.

The gateway returns HTTP 503 while schema and permission checks are pending.
Successful reloads update the session file's fingerprint and readiness state.
An invalid pending SQL migration rolls back; correct the file to retry.
Editing or deleting an applied migration is refused: restore its original
contents and add a new migration instead. If a migration commits but fails
permission/RLS checks, the gateway stays paused; add a subsequent migration to
repair the schema. The watcher reports each failed source version once and
retries when files change. Client-check failures leave the validated API
available and report that checks failed. With `--json`, stdout contains one JSON
event per readiness/reload transition; events never include test tokens.

Before regenerating types, startup and reload compare the new schema with the
last accepted local contract using the same rules as `data-api diff`. Reports
identify removed relations, columns and RPCs; changed field/argument/return types;
new required fields or arguments; nullability, write-access, relationship and enum
changes. A rename appears as a removal plus an addition. Normal development
reports breaking changes and continues after schema/RLS validation.

The last accepted contract is saved privately to `.gregale/data-api-contract.json`
and survives shutdown. The first run initializes it. To reject breaking changes,
use `--check-breaking`; it requires an existing contract so a verification run
cannot silently initialize its own baseline. A rejected reload keeps the gateway
paused and preserves both the accepted contract and generated types. SQL has
already committed locally: add a new compatible migration to repair the schema,
or explicitly revise your chosen baseline and restart development.

For CI, save a baseline outside the ignored `.gregale` directory and select it
explicitly. Selected baselines are read-only and remain fixed across reloads:

```sh
gregale data-api dev --once
cp .gregale/data-api-contract.json schema.json
gregale data-api dev --once --check-breaking --baseline schema.json
```

Commit `schema.json` alongside the application when using it as a CI baseline.
`--baseline` paths are relative to the selected project. `--once --check-breaking`
exits nonzero on breaking changes, removes disposable services, and preserves
the baseline and types. Compatibility reports also appear as `compatibility`
JSON events with fingerprints, change paths/reasons, and a breaking count.
The session receipt includes the latest comparison. Offline comparisons use:

```sh
gregale data-api diff --baseline schema.json --current candidate.json --check
```

Both files use the versioned JSON format from `data-api types --snapshot` or the
local saved contract. Offline diff validates contract shapes and fingerprints
and requires no login, database connection, or running development services.

Ctrl-C, SIGTERM, normal exit, and startup failure remove the session's containers,
anonymous PostgreSQL volume, temporary runtime, and identity file. Generated
types and the accepted local contract stay in the project. Each session starts
with an empty database. If Docker becomes unavailable during cleanup, the command
prints exact container removal commands.
A force-killed process cannot clean up; remove its containers marked
`gregale.data-api.dev=true` manually with their volumes. Local checks do not
qualify a managed provider or staging deployment.

## Develop your own schema

`data-api dev` reads `data-api.dev.json` at the project root, or an explicit
`--config FILE`. The [orders example](../examples/data-api-orders/) is a runnable
project with no notes table or starter client dependency:

```sh
cd examples/data-api-orders
gregale data-api dev --port 0 --once
```

```json
{
  "version": 1,
  "migrations": "migrations",
  "seed": "seed.sql",
  "output": "src/database.types.ts",
  "identities": {
    "buyer": { "subject": "customer-a", "claims": { "tenant": "shop-a" } },
    "other": { "subject": "customer-b", "claims": { "tenant": "shop-b" } }
  },
  "verify": { "collection": "data-api.requests.json", "scenarios": ["orders-isolation"] },
  "check": { "directory": "web", "commands": [["npm", "run", "typecheck"], ["npm", "test"]] }
}
```

Paths are relative to the project. Migration, seed, verification and check paths
must stay inside it. `--output` overrides the configured output. Identity names
are available in saved requests and the inspector; subjects must include at
least two distinct users. Custom claims cannot replace reserved JWT fields,
including `sub` or `role`. Tokens remain private to the disposable local session.

Required scenarios must exist and exercise two distinct subjects. Authentication
rejection and every required assertion must pass before readiness and after
migration reloads. Failed reloads keep the gateway paused and preserve the last
accepted contract. Project authors should test cross-user reads, writes and RPCs;
passing scenarios verify those assertions rather than every possible policy.
Use captures and cleanup steps so checks can run repeatedly against the same
local database. `--replay` runs additional selected scenarios after required
verification.

Seeds run once after initial migrations in an owner transaction, with a 15-second
statement timeout. Only test data belongs in seeds and saved requests. Restart
dev after changing configuration, identities or seeds. The watcher tracks the
configured migration directory. `--check` runs the configured argument arrays
without a shell; defaults remain the starter client's typecheck and tests.
Projects without a config retain legacy notes verification. Newly initialized
starters include their explicit project config, excluded from deployment uploads.

## Hosted owner workspace

Open **Data API** from an app's dashboard. The workspace shows managed Data API
binding status and offers **Inspect schema**, **Generate TypeScript**, and
**Refresh Data API**. Schema and type operations require a deployed `data-api`
runtime and enabled app tasks; each action runs the existing catalog generator
inside the deployed app. No database credentials or application tokens are
needed in the browser.

The schema view lists tables, views, columns and approved RPC signatures. A
completed export provides a `database.types.ts` download and fingerprint.
Pending tasks refresh the page; terminal results stop polling. Failed, truncated,
malformed or previous-deployment output cannot become a downloadable contract.
Results describe the schema at the time of the operation: regenerate after
migrations. Refresh uses the existing fresh restart and progress page; processing
completion still requires an application health check using its JWT.

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
nullability, defaults, generated columns, foreign-key relationships and named
composite types in exposed schemas.
Generated `Insert` and `Update` types exclude generated-always fields.
Unrecognized PostgreSQL relation types become `unknown`. Supported opted-in
RPC signatures are generated, including supported named composite arguments and
results. Numeric types become JavaScript `number`, so values beyond
JavaScript's exact numeric range need an application-specific representation.

For example, a PostgreSQL `CREATE TYPE api.address AS (city text, postcode text)`
generates `Database['api']['CompositeTypes']['address']`. Use the generated helper
for application values:

```ts
import type { CompositeTypes } from './database.types.js'
const address: CompositeTypes<'address'> = { city: 'Istanbul', postcode: null }
```

Nested composites and arrays reference their generated types. Composite fields
are nullable, including domain attributes: an empty composite RPC result expands
to an object of null fields. All object keys are required in the generated type.
Types without schema/type usage are excluded; inaccessible or unsupported nested
fields become `unknown` and exclude RPCs that depend on them. Standalone
composites are types rather than REST resources. Table row types continue to use
the relation contract filtered by column privileges.

New schema exports use normalized snapshot version 3 inside the unchanged
version 1 contract envelope. The current CLI can also compare version 1 and 2 baselines.
Upgrade the CLI before consuming new exports, regenerate types after upgrading
the runtime, and review the diff before approving a new baseline. A format-only
upgrade without new composite or result-relation metadata produces no structural changes, though
its fingerprint changes.

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
such a relationship could infer a same-named, unrelated local table. Ordinary
views inherit relationships when every foreign-key or referenced-key column is
projected directly and remains readable. Renamed columns and view chains are
supported within the same exposed schema. For example:

```sql
CREATE VIEW api.order_summaries WITH (security_invoker=true) AS
SELECT id AS order_id, customer_id AS buyer_id, total FROM api.orders;
```

```ts
const orders = await db.from('order_summaries')
  .select('order_id,buyer:customers!orders_customer_id_fkey(name)')
```

The example assumes a declared `orders_customer_id_fkey` foreign key and grants
for the serving binding. Joins can target tables or views. Foreign-key column
order and aliases come from PostgreSQL's recorded origins, and one-to-one
cardinality follows the underlying key constraint as PostgREST does. View columns
retain their conservative nullable types; views remain read contracts in the
generated helpers. Use security-invoker views and RLS on every underlying table.

Computed key expressions, incomplete composite keys, unions, inaccessible
origins, materialized views and unsupported definitions do not gain inferred
relationships. Chains require readable objects in exposed schemas; cross-schema
paths remain omitted. SQL-defined relationships use the explicit contract below.
Multiple foreign keys require
constraint hints. Repeated aliases of a composite key may still return
`PGRST201` even with a constraint hint; avoid duplicating key projections when
you need one unambiguous embed. Inference preserves the engine's alternatives
and does not validate dispatch hints. View definitions are never exported;
inspection fails if their combined input or expanded relationship metadata
exceeds existing generation bounds.

Changing a view's key origins can change its relationship metadata and schema
fingerprint without changing the column types. Refresh the runtime, regenerate
types and review the structural diff after changing a view.

The starter qualifies many-to-many
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

## Typed computed relationships and RPC projections

Use an opted-in SQL function to define a relationship without a foreign key:

```sql
CREATE FUNCTION api.latest_invoice(source_row api.orders)
RETURNS SETOF api.invoices ROWS 1
LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
AS $$
  SELECT i.* FROM api.invoices i
  WHERE i.subject=source_row.subject AND i.order_id=source_row.id
  ORDER BY i.id DESC LIMIT 1
$$;
REVOKE ALL ON FUNCTION api.latest_invoice(api.orders) FROM PUBLIC;
COMMENT ON FUNCTION api.latest_invoice(api.orders) IS '@gregale:rpc';
GRANT EXECUTE ON FUNCTION api.latest_invoice(api.orders) TO your_data_api_login;
```

The existing owner permission dry-run/apply workflow can grant the annotated
function instead of the final manual `GRANT`. Keep RLS on source and target
tables; use `security_invoker=true` for views. Function bodies remain
owner-authored code and must enforce the intended relationship under the
verified JWT subject. Passing a row to an RPC does not prove the caller owns it.

```ts
const orders = await db.from('orders')
  .select('id,latest:latest_invoice(id,total)')
// latest is the selected invoice object or null.

const invoices = await db.rpc('search_invoices', { min_total: 100 })
  .select('id,total,orders(id)').order('total').limit(10)
// search_invoices must return SETOF api.invoices; its result is an array.
```

Generated source/target metadata lets the SDK infer aliases, nested joins,
`!inner`, filters and projections. A computed relationship requires exactly one
named, non-defaulted input of a fully readable exposed relation row type, with
recognized field types, and a `SETOF` fully readable relation result. The source,
target and function must share one exposed schema. Functions must be security
invokers, `STABLE` or `IMMUTABLE`, uniquely named without overloads, executable by
the binding and annotated exactly `@gregale:rpc`. Tables and ordinary readable
views work as endpoints. Names cannot shadow an exposed relation, foreign-key
constraint or source column. Overrides, cross-schema relationships, scalar
computed fields, materialized/foreign-table endpoints and unsupported signatures
are outside this contract.

PostgREST uses `ROWS 1` for an object-or-null embed; other estimates produce
arrays. Declare `ROWS 1` only when the body returns at most one row. A direct
POST RPC call still returns an array, including for `ROWS 1`. The generated
client accounts for both shapes; `.single()`/`.maybeSingle()` request object
responses explicitly. Approved ordinary relation-set RPCs gain target metadata
for nested projections too, without becoming computed relationships.

Relationship functions can run inside `select`, outside the RPC route guard.
Inspection and fresh startup therefore reject executable, engine-recognized
relationship signatures that are not supported and opted in. Owner permission
checks report `computed_relationship_not_approved` blockers and refuse apply
until the serving role loses those grants, including inherited `PUBLIC`
execution, or the function is made eligible. Private functions with execution
revoked are omitted. Check permissions and fresh-restart after changing
annotations, signatures, grants or security modes; this check assesses the
current serving grants at inspection/startup and does not continuously monitor
owner changes to them.

Normalized snapshot version 3 carries optional function `setof` metadata with
source, target and embed cardinality. Upgrade the CLI, runtime and owner
dashboard together; the current parser accepts version 1 and 2 baselines.
Regenerate types and review fingerprints after upgrading. Removing or changing
existing result metadata is a breaking structural diff, even when `ROWS`
changes leave the RPC's array return type unchanged. Adding result metadata is
compatible. The local inspector, owner dashboard and OpenAPI extensions show
the same relationship contract without function bodies or catalog identifiers.

## JSON and JSONB projections

PostgREST exposes PostgreSQL JSON paths directly, including aliases, nested
keys and array indexes. Generated JSON/JSONB columns stay `Json`; a database
column does not describe the arbitrary keys stored inside it. Even a NOT NULL
JSON column can have missing keys. The native SDK's JSON-path inference can
omit this SQL nullability, so use the explicit projection helpers:

```ts
import { selectJson, jsonText, jsonValue } from '@gregale/data'

const orders = await selectJson(db.from('orders'), 'id', {
  status: jsonText('metadata', 'status'),
  city: jsonText('metadata', 'delivery', 'city'),
  lastItem: jsonText('metadata', 'items', -1, 'name'),
  details: jsonValue('metadata', 'details'),
}).eq('metadata->>status', 'paid').order('id').limit(10)
// id retains its generated type; text aliases are string | null.
// details is arbitrary Json, including null.
```

`selectJson` accepts table/view builders, write builders and approved
relation-set RPC builders. Its base selection is the normal literal PostgREST
select string, so plain columns and FK/computed joins keep their SDK inference.
Use `''` for JSON aliases only. Put JSON paths in the alias map rather than the
base selection to receive the nullable helper types. A source column must
exist in the generated row contract. Alias names must be known to TypeScript,
and type checks reject aliases that replace base fields.
The helper generates the select expression and returns the existing SDK builder;
filters, `.single()`, `.maybeSingle()`, tokens, RLS and SQL column grants apply
normally. Calling `.select()` again replaces the projection.

`jsonText` ends its path with `->>` and always yields `string | null`. JSON
numbers and booleans become text; objects and arrays become serialized JSON
text. `jsonValue` uses `->` and yields `Json`. Keys may contain spaces, commas or
dots; names are quoted. Numeric strings select object keys, while safe integer
numbers select array indexes (negative indexes count from the end). Empty keys,
quotes, backslashes and control characters are rejected before sending. No JSON
shape, key presence or value enum is inferred. Validate results in application
code before assigning a narrower shape; SDK overrides are type assertions.

| Stored value or path | JSON extraction (`->`) | Text extraction (`->>`) |
| --- | --- | --- |
| Missing key, invalid traversal or SQL NULL | SQL NULL, encoded as `null` | SQL NULL, encoded as `null` |
| JSON null | JSON null, encoded as `null` | SQL NULL, encoded as `null` |
| JSON string `"null"` | String `"null"` | Text `"null"` |
| JSON number `42` | Number `42` | Text `"42"` |

HTTP JSON cannot distinguish an extracted JSON null from SQL NULL. For a JSONB
column, `.is('metadata', null)` selects SQL NULL and
`.eq('metadata', 'null')` selects stored JSON null. For a path,
`.is('metadata->>status', null)` also matches missing keys and JSON null values;
it does not match a JSON string containing `"null"`. Text path filters such as
`.eq('metadata->>status', 'paid')` work for both JSON and JSONB. JSONB equality
and containment operators do not apply directly to PostgreSQL `json` columns.

The local inspector shows JSON path examples for readable JSON columns. Its
requests use the selected test identity. JSON projections do not bypass source
column grants or the relation's RLS, including through security-invoker views,
approved RPCs and write representations. Upgrading the SDK provides these
helpers without changing the normalized schema format or fingerprints.

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

Configure an application JWT in `GREGALE_DATA_API_ACCESS_TOKEN` through your
local environment or CI secret injection before sync. Its issuer, audience and
subject must match the Data API's normal application authentication. An account
API key is not an application JWT. The token must remain valid through the
workflow; sync does not mint or renew it. The optional `access_token_env` config
property selects a different environment variable name. Keep token values out
of command arguments and workflow JSON.

Sync validates the complete configuration and resolves the Data API's HTTPS
health URL, then runs the migration command, an optional permission command, a fresh restart
with completion and readiness checks, private type generation with an atomic
file write after serving-contract verification, and the client check. A failed permission command prevents refresh,
type export and the client check. A failed step exits nonzero and prevents later
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
task/deployment IDs, schema fingerprint, `contract_verified: true` and output path. Use sync in migration
jobs. Keep `data-api types --check` in application CI as the read-only drift
check; it never runs the migration or refresh steps.

## HTTP contract and bounds

Relation CRUD lives at `/rest/v1/TABLE`. Supply `Authorization: Bearer JWT`.
The authenticated OpenAPI endpoint keeps PostgREST's Swagger 2 format and adds
schema-derived `schema.relation.Row`, `Insert`, and `Update` definitions, plus
approved RPC argument and return schemas. These use the same normalized catalog
as TypeScript generation; `x-gregale-schema-fingerprint` identifies that startup
contract. `Accept-Profile` selects an exposed schema. Nullable values use
`x-nullable`; required insert fields exclude defaults, identity and generated
columns. Default SQL expressions are not exported. JSON and unsupported database
types remain unconstrained, matching `Json` and `unknown` in TypeScript.

Write bodies reference the single-row schema; `x-gregale-bulk-schema` describes
bulk bodies because Swagger 2 cannot represent their union. Response schemas
describe full rows with array media types and `Prefer: return=representation`;
projections, embedded relations and object media types can change their shape.
The document also describes JWT authentication, filters, pagination, gateway
problem responses and `X-Request-Id`. Database errors retain PostgREST's error
format. RPC routes advertise POST only. Refresh the running app after migrations
to update this contract, as with generated types and the serving fingerprint.

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
scalar-set, named-composite or exposed-relation-set results. Named composites
return objects; `SETOF` composites return arrays. Composite arguments accept JSON
objects, and nested fields use the generated contract. Defaults make arguments optional;
PostgreSQL permits null arguments and scalar results. Eligible computed
relationships may accept their source relation row. Variadic, unnamed,
OUT/INOUT, unsupported or inaccessible composite dependencies, and non-set
relation-row signatures are excluded.
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

### RPC permissions in the sync workflow

New starters run `node migrations/release.mjs` as their release command (both
manifest and Procfile). It commits migrations, then applies supported opted-in
RPC grants and checks readiness. The starter's existing `deploy --wait` migration
command waits for this entire release task, so `data-api sync` refreshes only
after permission setup succeeds. Migration credentials remain in the remote
release task; no local database URL is required. During rotation overlap, use an
explicit `--role` argument in the owner-controlled release command, or wait for
the old login to retire. Permission failure does not undo committed migrations.

Existing starter projects must update their release command and include the
new release and permission tooling; regenerating a config alone does not change
a deployed release task. `npm run migrate` remains a migration-only command.

Custom workflows can add an optional `permissions` command to `data-api.json`:

```json
"permissions": {
  "directory": ".",
  "command": ["npm", "run", "rpc:permissions", "--", "--apply"]
}
```

This property belongs alongside `migrate` and `check` in the workflow object.
Sync validates its executable and directory before any work, and runs it after
migration and before refresh under the same deadline and cancellation handling.
Commands run locally with the existing environment; this example requires an
owner-controlled local migration connection configured outside the JSON. For
managed bindings, use a command that waits for a remote owner task instead, or
include permission setup in the migration release as the starter does. Omit the
property when the release task already handles it. Existing configs without
`permissions` retain their previous behavior. A permission preview exits nonzero
when grants are missing; use `--apply` to complete grant setup.

### Verify the serving contract

The runtime's authenticated `GET /__gregale/schema` returns only
`{"ready":true,"version":1,"fingerprint":"<64 lowercase hex characters>"}`.
It uses the same application JWT verification as data requests and requires
PostgREST readiness. Responses are not cached. `/healthz` remains an
unauthenticated readiness probe and exposes no fingerprint or schema details.

The fingerprint uses the same normalized catalog as type generation and is
captured once at runtime startup, alongside the RPC allowlist. It is never
recomputed from the database on a diagnostic request. Column/type, relationship
and supported RPC metadata changes (including binding EXECUTE grants that change
RPC eligibility) can therefore reveal a stale runtime. Function bodies, RLS
policy semantics and default values that preserve the type contract are not
covered by this fingerprint.

After refresh and private type export, `data-api sync` requests this endpoint
at the app's canonical HTTPS origin, falling back to its platform URL. It sends
only the application token, never the account key, and refuses redirects. A
missing, unauthorized, unready, malformed or mismatched response fails sync
before writing types or running the client check. Existing types are retained;
committed migrations and accepted remote operations remain. Deploy the current
runtime when an older version returns 404, or coordinate schema changes and
rerun a fresh refresh when fingerprints differ. Configs now require the
application JWT environment input even when they omit a `permissions` step.

This verifies equality of the captured startup catalog and exported type
contract while the SQL engine is ready. Coordinate owner DDL with deployment;
it is not a transaction spanning the catalog inspection, engine cache load and
export, nor proof of two-user policy correctness or every serving replica.
Live staging qualification remains required.

### Request IDs and runtime logs

Every request reaching the Data API handler gets a new gateway-generated UUID
in the `X-Request-Id` response header. Caller-supplied IDs are ignored; upstream
response IDs cannot replace it. Allowed browser origins can read this header
through CORS. Gateway-generated problem responses also include `request_id`.
PostgREST responses retain their original bodies; use the header to correlate
those errors. An aborted request may close before the client receives headers.

The gateway writes one JSON record to stderr when each response finishes or
closes early. Records contain only `time`, `level`, `event: "data_api_request"`,
`request_id`, an allowlisted HTTP `method`, a fixed `route` category, `status`,
monotonic `duration_ms`, `outcome` and an allowlisted gateway `code` (or null).
Route categories are `health`, `schema`, `openapi`, `rpc`, `rest` and `unknown`;
no table, function or full path is recorded. A completed response records its
HTTP status. An aborted response records the sent status, or null if headers
were never sent, and `outcome: "aborted"`. A successful local response finish
is not proof that the client received every byte.

Authentication rejection, gateway query timeout and engine unavailability are
identified as `authentication_failed`, `query_timeout` and `unavailable`.
Rejected upstream HTTP responses use `upstream_error`; the gateway does not
parse database error bodies to classify SQL errors or server-side timeouts.
These logs never record JWTs, SQL roles, subjects, database URLs, credentials,
request/response bodies, headers, origins, schema names, query strings or raw
exception text. Startup configuration and engine spawn failures use fixed JSON
error events. PostgREST process output remains suppressed.

Use the response ID to find the matching record in your app logs. Logging
exceptions do not change the response. These are gateway request records, not
distributed traces or measurements of platform wake latency. Log retention and
live staging verification remain deployment concerns.

### Runtime contract guard

Generated TypeScript exports `schemaFingerprint` alongside the application
types. Use it to opt in to an exact serving-contract check for reads, writes
and RPC calls:

```ts
import { createDataClient } from '@gregale/data'
import { schemaFingerprint, type Database } from './database.types.js'

const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  accessToken: () => session.getAccessToken(),
  expectedSchemaFingerprint: schemaFingerprint,
}).schema('api')

const result = await db.from('notes').insert({ subject: session.user.id, body: 'Hello' })
if (result.error?.code === 'schema_contract_mismatch') {
  // Resolve the client/deployment mismatch before submitting the write again.
}
```

The SDK validates the fingerprint as 64 lowercase hexadecimal characters and
routes requests through `/__gregale/contract/<fingerprint>/rest/v1/...`.
After verifying the application token, the gateway compares it with the
immutable contract captured at startup. A mismatch returns HTTP 409
`schema_contract_mismatch` without contacting PostgREST. An unavailable serving
fingerprint returns HTTP 503 `serving_contract_unavailable`. HEAD responses have
no JSON body; use the status. Request IDs, CORS, RLS, grants, profile headers,
query typing and native error handling continue to apply. Guarded OpenAPI
documents retain the qualified base path.

The distinct path matters during upgrades: older runtimes reject it with 404
instead of ignoring an optional header and executing a write. Upgrade the
runtime before enabling the option. Clients never fall back to an unguarded
route, automatically accept a new fingerprint, or replay rejected writes.
Clients without the option retain `/rest/v1` behavior. No extra schema fetch or
per-request database inspection is required.

This is an exact serving-contract check. Additive schema changes also require
regenerated types and a coordinated client rollout when the guard is enabled.
Uncoordinated database DDL or an engine-only cache reload can make the captured
contract stale; use `data-api sync`/fresh runtime refresh and release verification.
The guard does not establish behavioral or authorization compatibility or make
HTTP requests atomic with database migrations. Structural compatibility checks
and application authorization tests remain separate checks.

### Accept older clients during a compatible rollout

Exact matching remains the default. To allow a selected older client contract,
compare its saved snapshot against the intended serving snapshot:

```sh
gregale data-api diff --baseline old-schema.json --current new-schema.json \
  --accept-contract accepted-contracts.json
```

`--accept-contract` writes an owner policy atomically only when there are **zero
structural breaking changes**. Reviewed breaking-change approvals do not waive
this requirement. Failed checks leave an existing output untouched. The output
must differ from all comparison inputs. Identical contracts need no policy.
You can also compare against an owner-authenticated app instead of `--current`.

The policy has this shape (substitute the actual generated fingerprints):

```json
{
  "version": 1,
  "current_fingerprint": "<new serving fingerprint>",
  "accepted_fingerprints": ["<reviewed older fingerprint>"]
}
```

After reviewing application and authorization tests for the rollout, configure
`DATA_API_ACCEPTED_CONTRACTS` with the **JSON contents**, then deploy/refresh the
runtime. This is owner configuration, never a client-provided allowlist. To
accept several older versions, check each directly against the same current
snapshot and combine their reviewed fingerprints into one policy. Acceptance
is not transitive. Clients keep their existing `expectedSchemaFingerprint`.

The runtime validates the policy and captures it at startup. Malformed, duplicate
or self-referential entries and a `current_fingerprint` that differs from the
inspected serving contract prevent startup before PostgREST is launched. A later
schema change requires a new comparison and policy; an old policy cannot silently
carry forward. Authentication, RPC eligibility, grants and RLS still apply.
Unapproved fingerprints receive 409 before SQL executes. OpenAPI keeps the
requested qualified route, while `/__gregale/schema` advertises the actual
current fingerprint. Remove the policy and refresh to revoke older contracts.

Structural comparison does not cover policy semantics, function bodies,
default behavior or every application assumption. Acceptance authorizes old
clients to use the current API; it does not restore an older response schema.
Use explicit projections when your application depends on exact response keys.
This policy shares the startup-contract guard's live-DDL limitation.

### Test contract rollouts locally

Load the reviewed policy without staging credentials:

```sh
gregale data-api dev --directory . --accepted-contracts accepted-contracts.json
# CI, with pinned old/current clients in the request collection:
gregale data-api dev --directory . --accepted-contracts accepted-contracts.json \
  --once --replay rollout.requests.json
```

The policy path is relative to the project, must be a bounded regular file, and
must differ from generated types, baselines and other workflow inputs. Invalid
policies fail before services start; stale policies fail before types or contracts
are saved. With watching enabled, edits to the policy trigger the same serialized
reload and authorization checks as migrations. A stale policy pauses the gateway
and preserves the disposable database, identity tokens, saved contract and types.
Save a policy reviewed against the migrated schema to recover. Deleting or
corrupting the configured file also pauses requests. To return to exact matching
without an acceptance list, restart without `--accepted-contracts`.

Each saved request may include an optional `expectedSchemaFingerprint` containing
64 lowercase hexadecimal characters. Paths remain relative `/rest/v1/...` paths;
replay builds the guarded route, preserves query parameters, and never falls back.
Use the old fingerprint for old-client requests, the current fingerprint for
current clients, and an unapproved fingerprint with an expected 409 to prove
rejection. Test RLS using distinct identities in the same scenario. The inspector
can send, save and load pinned requests too. Unpinned collections keep their
existing behavior; collection versions 1 and 2 remain supported.

### Typed client response diagnostics

`createDataClient` accepts an optional `onResponse` callback so applications can
capture IDs without replacing fetch:

```ts
const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  accessToken: () => session.getAccessToken(),
  onResponse: ({ requestId, status, durationMs }) => {
    console.info({ requestId, status, durationMs })
  },
}).schema('api')
```

The exported readonly `DataResponseInfo` type has only `requestId`, `status` and
`durationMs`; instances are frozen. IDs must be UUID v4 headers, otherwise they
are null, including when a browser cannot read the header through CORS. Tokens,
URLs, query values, bodies, headers and subjects are excluded. The SDK itself
does not log these records. The starter's `notesClient` and `readBrowserNotes`
helpers pass through this option.

The callback fires per HTTP response, including failed HTTP responses and each
retry attempt, for reads, writes and RPCs. Concurrent calls keep their own
metadata. The measured client time includes token resolution and fetch through
response headers, but excludes response-body processing; it is not SQL execution
or a platform wake metric. No callback fires for failures with no response.
Body parsing, counts, errors, retry behavior and generated type inference are
unchanged. Callback exceptions and rejected promises are isolated; asynchronous
callbacks are not awaited. Keep synchronous callbacks short and handle telemetry
retention and delivery in the application.

The [staging canary](../tests/data-api/staging/README.md#complete-workflow-coverage)
now checks the integrated development loop: owner-managed RPC grants, three sync
runs (initial setup, schema change and credential replacement), serving/exported
fingerprint agreement, typed CRUD/RPC subject isolation, and SDK IDs matching
runtime logs. Its evidence contains identifiers and verification results rather
than raw logs or credentials. The local harness tests include a simulated full
journey and failure cleanup; a live qualified staging target is still required
before this can provide release evidence.

## Check schema compatibility

Save the approved contract as a JSON baseline in source control:

```sh
gregale data-api types notes-data --snapshot --output schema.json
```

After applying migrations to a candidate database, compare its Data API app with
that baseline before promoting the application change:

```sh
gregale data-api diff notes-data --baseline schema.json
gregale --json data-api diff notes-data --baseline schema.json --check
```

Diff inspects the current database with a bounded, owner-authenticated private
app task using the restricted Data API binding. It does not refresh the serving
app or run migrations. The runtime must include snapshot export support; older
runtime deployments fail rather than report a compatible schema. The baseline
is validated before starting a remote task and is never rewritten by diff.

Without `--check`, a completed comparison exits zero even if it finds breaking
changes. With `--check` (or its `--check-compatible` alias), unapproved breaking changes exit one after printing the report;
invalid baselines and failed inspections also exit nonzero. JSON output includes
baseline/current fingerprints, task/deployment identifiers, the breaking count
and a deterministic array of changes. The `approved` and `unapproved` counts
partition the total `breaking` count. No changes produces an empty array.

Removed schemas, relations, columns, RPCs, RPC arguments, composite types and
composite fields are breaking. Composite field type/nullability changes and new
required object keys are also breaking, even when the containing table or RPC
still references the same named type. New composite types are compatible.
Type changes (including enum changes), nullability changes in either direction,
removed write privileges, table/view changes, removed or modified relationships, newly
required insert fields, and required RPC arguments are conservatively breaking.
Optional fields, new relations/RPCs, optional RPC arguments and relaxed write
requirements are compatible. Defaults are checked through insert optionality;
changing a default expression while retaining optionality is not detected.

Relationship array order and JSON object key order do not constitute changes.
Reports identify each relationship by constraint name and target. A new connection
is compatible unless it could make an existing unqualified embed or foreign-key
column/name hint ambiguous. A second connection between the same relations is
conservatively breaking, including a connection in the reverse direction.
Repeated view aliases or other metadata that cannot be matched uniquely retain
a conservative relation-level report.

Update queries, writes and generated types for breaking changes, then approve a
new baseline explicitly. This is a structural contract check: policies, function
bodies, default values and data-dependent behavior need separate tests. It does
not verify the serving fingerprint; use `data-api sync` for that release check.

### Approve an intentional breaking migration

Keep the existing baseline when you want CI to retain a visible record of an
intentional break. First obtain the candidate contract and JSON report:

```sh
gregale data-api types notes-data --snapshot --output candidate.json
gregale --json data-api diff --baseline schema.json --current candidate.json
```

Create a reviewed `compatibility-approval.json` using the report's exact
fingerprints, paths and reasons. Include only the breaking changes you intend
to accept, with a rationale for each:

```json
{
  "version": 1,
  "baseline_fingerprint": "<64 lowercase hex characters from the report>",
  "current_fingerprint": "<64 lowercase hex characters from the report>",
  "changes": [
    {
      "path": "api.notes.priority",
      "reason": "column removed; update projections and writes",
      "rationale": "The deployed client no longer reads or writes priority."
    }
  ]
}
```

Commit the approval alongside the baseline and migration, then check the candidate:

```sh
gregale --json data-api diff --baseline schema.json --current candidate.json \
  --check-compatible --approval compatibility-approval.json
gregale data-api dev --once --baseline schema.json --check-breaking \
  --approval compatibility-approval.json
```

For local development, approval paths are project-relative; an explicit baseline
and `--check-breaking` are required. Diff can also apply approvals during hosted
inspection. It reads and validates the baseline and approval before starting a
remote task. Both commands leave the baseline and approval file unchanged.

An approved change remains `breaking: true` and gains `approved: true`; it is
printed as `approved breaking` in text reports. The check succeeds only when
every breaking change is approved. Missing files, invalid JSON, duplicate or
unmatched path/reason pairs, missing rationales, and stale fingerprints fail.
Even an unrelated schema change invalidates the candidate fingerprint: review
the full report before replacing the approval. There are no wildcard approvals.
An approval records an intentional break; it does not establish compatibility
for clients that still depend on the removed contract or bypass authorization tests.

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

The starter's `notesClient.cursorPage()` uses the SDK's `cursorPage` and
`cursorFrom` helpers. Its public `{ created_at, id }` cursor format and strict
timestamp/id validation remain the same, including PostgreSQL microseconds.
Upgrade the installed SDK bundle alongside the starter to use these helpers.

### Coordinated hosted schema updates

Opt-in coordinated sync keeps ordinary SQL traffic paused until compatibility,
client and authorization checks finish. Install its private database barrier
before enabling the runtime. Use a materialized `data-api` template as the local
owner tool directory; install its pinned Node dependencies there:

```sh
gregale init data-api-runtime --template data-api
npm ci --prefix data-api-runtime
# Configure MIGRATION_DATABASE_URL locally with owner credentials.
node data-api-runtime/refresh.mjs install YOUR_BINDING_LOGIN_ROLE
# Deploy the current runtime to every instance using this binding first.
gregale env set --app notes-data DATA_API_COORDINATED_REFRESH=1
gregale data-api refresh notes-data --wait
gregale data-api types notes-data --snapshot --output schema.before.json
```

The owner connection must be able to create the private control schema and
`SET ROLE` to the binding login for role-filtered inspection. Set matching
`DATA_API_SCHEMAS` in the local owner environment when exposing multiple schemas.
Do not expose `gregale_data_api` or grant serving credentials control-table
access. All runtimes sharing this role must enable coordination and expose the
same schemas; use separate binding roles for distinct exposures.

Extend the existing sync JSON with `coordination`:

```json
{
  "output": "web/database.types.ts",
  "migrate": { "directory": "migrations", "command": ["npm", "run", "migrate"] },
  "check": { "directory": "web", "command": ["npm", "run", "typecheck"] },
  "coordination": {
    "baseline": "schema.before.json",
    "pause": { "directory": "data-api-runtime", "command": ["node", "refresh.mjs", "pause", "YOUR_BINDING_LOGIN_ROLE"] },
    "resume": { "directory": "data-api-runtime", "command": ["node", "refresh.mjs", "resume", "YOUR_BINDING_LOGIN_ROLE"] },
    "authorization": { "directory": "web", "command": ["npm", "run", "test:data-api-authorization"] }
  }
}
```

Set an unpredictable private `GREGALE_DATA_API_REFRESH_TOKEN` (64 lowercase
hexadecimal characters, generated from 32 random bytes), keep it for recovery,
and configure the application JWT in `GREGALE_DATA_API_ACCESS_TOKEN` as before.
Do not commit either token or put it in a URL. An optional `token_env` selects
another local environment variable. Commands receive the private token under
`GREGALE_DATA_API_REFRESH_TOKEN`; client/authorization/resume commands also
receive `GREGALE_DATA_API_REFRESH_FINGERPRINT`. Only a client that sends both its
normal application JWT and `X-Gregale-Refresh-Token` can execute verification
requests while paused. The token grants no extra SQL or RLS permissions. Run
multi-user and multi-tenant denial scenarios with the respective application
identities; the token alone is insufficient. These are trusted owner checks,
not headers to ship in an ordinary browser application.

```sh
gregale data-api sync notes-data --config data-api.json
# After correcting a failed check or reviewing a breaking change:
gregale data-api sync notes-data --config data-api.json --resume-only
```

A normal run verifies the saved baseline against live inspection and the
serving runtime, acquires the durable pause, runs migrations/permissions,
refreshes the engine, and verifies the generated and serving fingerprints.
The baseline is compared structurally with the new snapshot. Unapproved
breaking changes stop the workflow; optional `coordination.approval` points to
an exact reviewed approval file in the existing `data-api diff` format. Existing
accepted-client deployment policies must also match the new runtime; stale
policies fail startup. The workflow then runs client and authorization checks
before releasing the barrier. Release re-inspects the live schema as the binding
role and rejects drift. The database records the verified fingerprint, so older
coordinated runtimes receive 409 before executing application SQL.

Pause waits for transactions admitted by the PostgREST pre-request hook,
including read-only GET transactions. Ordinary requests receive 503 while
paused; a queued request cannot slip through when the barrier commits.
Competing coordinators cannot replace an existing pause. The barrier is stored
in PostgreSQL and survives fresh restarts. `/healthz` remains a transport/engine
health check and adds `refresh_paused`; it does not mean ordinary SQL traffic
is admitted. The authenticated schema endpoint adds `refresh_protocol`,
`refresh_paused`, `refresh_verified` (whether this request owns the pause), and
`refresh_fingerprint`; it never returns the token/hash.

Migration, startup, compatibility, client, or authorization failures leave the
barrier paused. Recovery must use the same private token and baseline;
`--resume-only` skips migrations and permission setup, but repeats refresh and
all verification. No step automatically clears a pause on cancellation or
failure. If the resume command loses its acknowledgement, release may already
have committed: inspect the serving contract before taking further action.
Generated types are retained after a client-check failure, and completed DDL
is not rolled back. Losing the private token requires owner-controlled recovery
of the database barrier; the CLI does not adopt another session automatically.

This mode reserves one of the existing two connections for its control reader
and one for PostgREST. Engine-only SIGUSR1 reloads are rejected; use coordinated
sync. Owner/superuser DDL and direct database connections remain outside this
barrier. Serialize DDL through the workflow; fingerprints do not cover policy
bodies or every behavioral change. Upgrade every runtime sharing the binding
before relying on the barrier. Real hosted multi-replica qualification remains
pending until staging is configured.
