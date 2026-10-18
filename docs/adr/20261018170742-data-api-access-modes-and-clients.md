# ADR: Data API public reads, server keys, aggregates, single-app projects and Python/Go clients

Date: 2026-10-18
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR-900](900-allowlisted-data-api-rpc.md)

## Context

ADR-650 serves every data request with one verified application JWT. That
leaves five product gaps that customers hit in their first project:

- Catalog, pricing and landing-page data cannot be read without a user session.
- Trusted backends, jobs and webhooks cannot use the HTTP API. They need a
  separate SQL login and a second client for the same contract.
- Totals and grouped counts need a hand-written RPC per question.
- A project needs two apps: the Data API and a migration app whose only job is
  its release task. A Free account (one deployed app) cannot run the starter.
- Generated clients exist only for TypeScript.

The authorization authority must stay PostgreSQL privileges and row-level
security. The serving login keeps its ADR-650 shape: no memberships, no
ownership, no DDL and no RLS bypass. Account API keys still never authenticate
data access.

## Decision

### Access kinds

Every internal engine token now carries a gateway-owned `gregale_access` claim:
`user`, `anonymous` or `server`. The gateway deletes any caller-supplied value,
like the other reserved `gregale_*` claims. Policies read it from
`request.jwt.claims`. All three kinds use the same binding login as the SQL
role; no access kind can select another role.

**Public reads (`anonymous`).** Off by default. `DATA_API_PUBLIC_READ` lists up
to 64 schema-qualified relations in the exposed schemas. A request with no
`Authorization` header is anonymous only when it is a `GET` or `HEAD` of one of
those relations under `/rest/v1/`. Everything else without a token keeps
returning `401 token_required`: RPCs, OpenAPI, the serving contract, and writes.
An invalid token is never downgraded to anonymous.

Embedded resources in `select` must also be listed. The gateway parses the
`select` parameter's embedding syntax (aliases, `!hint`, `...` spreads) and
rejects unlisted relations, computed relationships and unparseable shapes with
`403 public_read_not_allowed` before dispatch. Column aggregates such as
`amount.sum()` and a bare `count()` are not embeddings.

Startup fails closed with `data_api_public_read_unsafe` unless each listed
relation exists in the serving contract and is either a table with RLS enabled
or a view with `security_invoker` set. Materialized views and missing relations
are rejected. This does not prove that the policies are correct. Anonymous
tokens carry no `sub`, so policies keyed only on the subject return no rows.
Owners grant anonymous rows explicitly, for example
`USING (published AND current_setting('request.jwt.claims', true)::jsonb ->> 'gregale_access' = 'anonymous')`.

Anonymous requests are always rate limited per client. The key is the trusted
`x-faas-client-ip` that the gateway stamps; IPv6 addresses are grouped by /64,
and a missing address shares one bucket. The owner's `DATA_API_RATE_LIMIT_*`
policy applies, or the canonical defaults (10/s, burst 20) when unset. The
anonymous limiter is separate from the subject limiter, so anonymous clients
cannot consume subject tracking capacity. Per-user concurrency, when enabled,
uses the same client key.

**Server keys (`server`).** Off by default. A trusted backend sends
`Authorization: Bearer gdsk_<43 base64url characters>`. The runtime stores only
SHA-256 hashes in two slots, `DATA_API_SERVER_KEY_A_SHA256` and
`DATA_API_SERVER_KEY_B_SHA256`. Either slot authorizes the request, which allows
rotation without downtime. Hashes are compared in constant time. A request that
also carries an `Origin` header is rejected with `403 server_key_browser_forbidden`,
so browser code cannot use a key by accident. This is defense in depth, not a
security boundary.

A server request may name a subject in `X-Gregale-Subject` (1–255 characters, no
control characters). The gateway then sets `sub` so ordinary user policies apply
on that subject's behalf. Without a subject, only policies that grant
`gregale_access = 'server'` match. Server keys never bypass RLS, because the
login cannot. Server requests skip per-subject rate limits and per-user
concurrency, but not HTTP admission or the total deadline. Logs record the
access kind and the matching slot, never the key, its hash or the subject.

The CLI generates the key locally, prints it once, stores its hash as a sealed
app secret in the free slot, and requests a fresh restart. It refuses to
overwrite a used slot unless asked to replace the oldest one. Revocation removes
a slot and restarts.

### Edge-cached public reads

`DATA_API_PUBLIC_READ_MAX_AGE` (1–3600 seconds, valid only with public reads)
lets the existing gateway response cache serve anonymous reads. The CLI owns one
named cache rule for `GET /rest/v1/*`. Requests with `Authorization` already
bypass the gateway cache, and only responses the runtime marks
`Cache-Control: public` are stored.

The gateway cache key is method, host, path and query, and it refuses responses
that `Vary` on anything else. The runtime therefore marks a response public only
when those fully determine it: a `200` for a default-schema relation, requested
without `Range`, `Range-Unit`, `Prefer`, `Cookie` or another `Accept-Profile`,
and with a JSON-compatible `Accept`. With caching on, anonymous responses use
`Access-Control-Allow-Origin: *` instead of `Vary: Origin`.

Each cacheable response carries `Cache-Tag: data-api` plus one
`data-api:SCHEMA.RELATION` tag for every relation in its body, embeddings
included, so a tag purge is complete. A response whose tags would exceed the
gateway's count or byte bounds stays `no-store`.

A request with representation headers that hits an existing entry receives the
canonical JSON. The data is public by construction, so this costs fidelity, not
confidentiality.

### Aggregates

`DATA_API_AGGREGATES=1` sets PostgREST's `db-aggregates-enabled`. It requires
`DATA_API_SQL_DEADLINE=1`, so an expensive aggregate is cancelled by the
per-request `statement_timeout` inside the existing 15-second budget, not only
by an HTTP abort. Startup rejects aggregates without the SQL deadline. Row,
response-byte, admission, rate and RLS bounds are unchanged.

### Single-app projects

`gregale data-api create --migrations DIR` and `gregale data-api deploy NAME
--migrations DIR` build one source archive containing the pinned runtime and the
project's migration directory. The archive declares a Procfile
`release: node migrations/release.mjs` (or `--release-command`). The same app
holds the serving `data_api` binding as `DATABASE_URL` and a `migration` binding
as `MIGRATION_DATABASE_URL`. Migration credentials keep their existing
release-task-only delivery.

A deployment therefore applies migrations, and its new instances load the new
schema at startup. The existing binding uniqueness rule `(app, scope, env key)`
already allows both bindings, and the server already reads Procfile release
commands, so no API or schema change is needed. Release scripts may import only
Node built-ins and the runtime's pinned `pg`. The two-app layout and coordinated
refresh remain supported.

### Python and Go clients

`gregale data-api types NAME --language python|go` renders a client from the
existing version-checked, fingerprinted contract snapshot (`--snapshot` task
output, or a local `--snapshot-file`). The CLI renders it; the runtime is
unchanged. Generated files use only the standard library, so no SDK has to be
published:

- Python 3.9+: `TypedDict` rows, inserts and updates; enum literals; and a
  client built on `urllib.request`.
- Go: structs with JSON tags, a gofmt-formatted file, and a client built on
  `net/http`.

Both clients support filters, ordering, ranges, insert, update, delete,
approved RPCs, user tokens and server keys. They never retry writes
automatically. They embed the schema fingerprint, and `--check` detects drift
exactly like the TypeScript export.

## Consequences

- Public reads and server keys widen who can reach SQL. Both are owner opt-ins
  with startup safety checks, explicit policies and per-client rate limits. They
  add no new SQL role, membership or provider credential mode, so the
  managed-PostgreSQL qualification contract is unchanged.
- Aggregates trade engine cost for fewer RPCs. They are only available together
  with the SQL deadline.
- A single-app project spends one app slot instead of two. Its release task can
  still fail after committing migrations; deployments do not roll schemas back.
- Python and Go output is generated code, not a published SDK, and has no
  runtime input validation beyond JSON decoding.
- New canonical bounds (`DataAPIPublicReadMaxRelations`,
  `DataAPIServerKeySubjectMaxBytes`, `DataAPIPublicReadMaxAgeSeconds`) live in `pkg/api/limits.go` and are
  mirrored in `config.mjs` under the existing parity test.

## Rejected alternatives

- **A Supabase-style `anon`/`service_role` role pair.** It needs role
  membership for the serving login, which ADR-650 rejected, and a
  provider-qualified credential mode.
- **Server keys that bypass RLS.** The login is `NOBYPASSRLS` by design. The
  `gregale_access` claim makes bypass an explicit, reviewable policy.
- **Anonymous access to every exposed relation.** It turns tables without RLS
  into public endpoints by default.
- **Rendering Python/Go clients inside the runtime.** It would tie client
  languages to runtime upgrades and add no information the snapshot lacks.
