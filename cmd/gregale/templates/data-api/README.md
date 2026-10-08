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
