# Schema-generated Data APIs

Data APIs expose PostgreSQL relations as authenticated REST endpoints and
generate TypeScript application types from the same schema. This is a managed
PostgreSQL preview: your account and region must already be enabled for managed
databases, `data_api` bindings, port 5432 egress, and app tasks.

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
can run with their owner's privileges. RPC functions are excluded in this version.

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
Unrecognized PostgreSQL types become `unknown`; RPC and composite types are
not generated. Numeric types become JavaScript `number`, so values beyond
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
gregale data-api refresh notes-data
# Wait for fresh-restart completion and verify the serving revision's /healthz.
gregale data-api types notes-data --output src/database.types.ts
```

Refresh requests the existing fresh app restart, which rebuilds the schema
cache. It may interrupt in-flight requests. Its success response confirms
restart admission, not completion; inspect the app's restart status and health
before using newly added fields. Use backward-compatible migrations during
rollouts. Type generation and `--check` read the current database; neither
command refreshes a live PostgREST cache.

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
