# PostgreSQL Data API

This template runs a schema-generated PostgREST API with application JWT
authentication. Create and configure it with `gregale data-api create NAME
--database DATABASE --issuer HTTPS_URL --jwks-url HTTPS_URL --audience AUDIENCE`.
Use the generated API at `/rest/v1/TABLE` and export application types with
`gregale data-api types NAME --output database.types.ts`.

Required environment values are `DATABASE_URL`, `DATA_API_ISSUER`,
`DATA_API_JWKS_URL`, and `DATA_API_AUDIENCE`. Managed bindings supply the first
value; the create command seals the rest. `DATA_API_SCHEMAS` defaults to `api`.
`DATA_API_ALLOWED_ORIGINS` is a comma-separated list of explicit browser origins.
Use application JWTs signed with RS256 or ES256, with a subject and expiration.

Schema changes belong in migration release tasks. Serving startup performs no
DDL. Define row-level policies using the subject in `request.jwt.claims` before
exposing user data. Use `gregale data-api refresh NAME` after migrations to
request a fresh restart, then regenerate the client types.

Type export includes named PostgreSQL composites in exposed schemas with schema
and type usage privileges. `CompositeTypes<'name'>` describes nested object
fields and arrays. Approved invoker RPCs can use these objects as inputs/results;
single composite results are objects and SETOF results are arrays. Composite
fields remain nullable, including domain attributes. Hidden dependencies become
`unknown` and exclude dependent RPCs. Upgrade the CLI before reading new
normalized version 3 snapshots; the current CLI also accepts version 1 and 2 baselines.

Ordinary views can inherit typed joins when they directly project every column
of a readable declared key. Renamed columns and view chains work within the
same exposed schema. Use security-invoker views and underlying RLS. Partial
keys, expressions, unions and inaccessible origins are excluded; ambiguous
paths still require PostgREST hints or an unambiguous view projection.

Annotated relation-set RPCs expose target metadata for typed projections and
joins. An eligible `@gregale:rpc` function with one named relation-row argument,
a `SETOF` relation result and `STABLE`/`IMMUTABLE` invoker semantics also defines
a computed relationship in the same exposed schema. `ROWS 1` embeds an object or
null; direct RPC responses remain arrays. Inspection/startup rejects executable
unapproved or unsupported relationship functions, including `PUBLIC` grants.
Revoke those grants or make the function eligible, then fresh-restart and
regenerate types. Names cannot shadow exposed relations, FKs or source columns.

Build with the included Dockerfile through Gregale's builder. For local
development, install dependencies with `npm ci --ignore-scripts`, provide
the restricted database connection and authentication settings, and set
`DATA_API_POSTGREST_BIN` to a local PostgREST 14.3 executable. Run `npm start`.
`npm test` checks runtime and generator behavior.

See [the complete guide](https://gregale.dev/docs/data-api) for migration,
authorization, client packaging, qualification requirements and bounds.

### Serving-contract diagnostics

`GET /__gregale/schema` requires a normal application JWT and PostgREST
readiness. It returns `ready`, `version: 1` and the normalized catalog fingerprint
captured at startup. Responses use `Cache-Control: no-store`. It does not expose
types, connection strings or application data; `/healthz` remains public.

`gregale data-api sync` compares this fingerprint with its private type export
before writing the file or running client checks. Configure the application
JWT through `GREGALE_DATA_API_ACCESS_TOKEN` (or the workflow's `access_token_env`).
The fingerprint describes the generated type contract, not policy semantics,
function bodies or an atomic view spanning engine cache loading and owner DDL.

### Request diagnostics

The gateway generates a fresh UUID for each handled request and returns it in
`X-Request-Id`. Incoming IDs are ignored. Allowed browser origins can read the
header. Gateway problem bodies include `request_id`; engine bodies are unchanged.

One JSON record is written to stderr on completion or early close, including
the ID, allowlisted method, fixed route category, HTTP status, duration and
outcome. Auth failures, gateway timeouts and engine unavailability have explicit
outcomes. Raw paths, table/function names, query values, headers, tokens,
subjects, database credentials and bodies are excluded. Upstream errors are
classified by HTTP status without inspecting their bodies. Canceled requests
use `aborted` and a null status if no headers were sent. IDs identify a gateway
request; these logs do not provide distributed tracing or platform wake timing.
