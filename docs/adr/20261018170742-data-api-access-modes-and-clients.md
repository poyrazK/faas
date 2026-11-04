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

### Server-key permissions

Each slot may carry `DATA_API_SERVER_KEY_{A,B}_PERMISSIONS`, a version-1 JSON
object with up to 4 KiB and 64 entries per list:

- `access`: `read` or `write`. Read-only keys allow `GET` and `HEAD`, and call
  only RPCs listed explicitly, because a function may write.
- `relations`: the relations a key may use, covering every relation embedded
  through `select`.
- `rpcs`: the functions a key may call.
- `subjects`: whether a key may send `X-Gregale-Subject`.

The gateway enforces permissions before admission, with
`403 server_key_permission_denied`, and RLS still applies to whatever they
allow. Keys with allowlists cannot read the full OpenAPI catalog.

Entries outside the exposed schemas never match, so permissions can only deny.
Permissions without a key are ignored rather than failing startup. The CLI
writes permissions before a slot's hash and removes them after it, so a key
never runs with another key's permissions or none at all.


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

### Live queries

RLS-mode live queries (ADR-20261016151947) set the same gateway-owned claims
as REST: `gregale_access` is `user`, and other `gregale_*` claims from the
token are removed. Live reads from the SDK decode values as the Data API's
JSON does, so generated row types apply to live rows. Anonymous and
server-key live subscriptions are not supported yet. They need endpoint
admission without a user token, and the public-read allowlist and key hashes
delivered to realtimed.

### Public RPCs

`DATA_API_PUBLIC_RPC` (at most 64 `schema.function` entries in exposed schemas)
lists approved functions callable without a token. Configure it with
`data-api create|configure --public-rpc` and remove it with `--no-public-rpc`.

- **GET only.** An anonymous call must be `GET` or `HEAD`; an anonymous `POST`
  to a listed function answers `405 public_rpc_requires_get` with
  `Allow: GET, HEAD`. PostgREST runs `GET` calls in a read-only transaction.
  That transaction, not volatility, is what stops writes: a `STABLE` function
  can call a `VOLATILE` one. Signed-in and server-key callers may use `GET` or
  `POST` on listed functions; other approved functions remain `POST`-only.
- **Startup check.** The runtime refuses to start
  (`data_api_public_rpc_unsafe`) unless every entry is in the serving contract
  (annotated `@gregale:rpc`, one signature, security invoker) and declared
  `STABLE` or `IMMUTABLE`. Volatility is checked by its own catalog query
  rather than added to the snapshot, so existing contract fingerprints do not
  change.
- **Row decisions.** Calls run as the binding login with
  `gregale_access=anonymous`. RLS applies to the tables the function reads.
  Unlike public relations, the gateway cannot prove the function reads only
  RLS tables or security-invoker views. Listing a function is the owner's
  statement that its body is safe for anyone; the docs say so.
- **Embedding.** Relations embedded through `select` must be in
  `DATA_API_PUBLIC_READ`, in the function's schema.
- **Limits and cache.** Public RPCs alone enable the anonymous per-address rate
  limit. `DATA_API_PUBLIC_READ_MAX_AGE` now requires either allowlist and
  covers canonical anonymous `GET` calls in the default schema. Responses carry
  `data-api-rpc:SCHEMA.FUNCTION` plus their embedded relations' tags; a
  function's own reads are not tagged. `configure` removes the cache rule and
  max age before it removes the last remaining allowlist.
- **Catalog and logs.** `/openapi.json` keeps the `get` operation only for
  public functions. Diagnostics count `public_rpc_requires_get` separately.

### Generated client parity (2026-10-19)

The Python and Go clients follow the TypeScript SDK's rules:

- **Anonymous access is explicit.** Without `anonymous=True` (Python) or
  `Anonymous: true` (Go), a client with no token refuses to send a request.
  Before this change, a client constructed without credentials sent anonymous
  requests silently. The clients are unpublished, so the behavior changed in
  place.
- **GET calls.** GET RPC arguments use PostgREST's query form: scalars as
  text, arrays as PostgreSQL array literals, objects as JSON. Null arguments
  are refused, because GET cannot distinguish NULL from the text "null".
- **Keyset pages.** The keyset algorithm, including NULL placement and
  reverse traversal for `before`, is the TypeScript `cursorPage` algorithm.
  A shared smoke test requires both clients to send identical requests.
- **NULL updates in Go.** Go update structs gain `SetNull` only when the
  relation has nullable updatable columns. The runtime checks names against
  that generated set.

### Anonymous SDK clients

`@gregale/data` gains an explicit `anonymous: true` option. With it,
`accessToken` is optional and may resolve to `null`; such requests carry no
`Authorization` header. A present token is still sent, and an empty,
whitespace-containing or non-string token still throws before the network.
Without the option, the client keeps refusing to send a request with no token:
a signed-out state is anonymous only when the application chose that, so an
expired session does not quietly downgrade to public data. The option is a
type-level union, so TypeScript rejects a nullable token source without it.

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

## Amendment 1 (2026-10-27): IPv6 visitors group by /48 under pressure

**Context.** Anonymous budgets key IPv6 visitors by /64 in a table of at most
`DataAPIRateLimitMaxSubjects` (4,096) subjects, and refuse new visitors with
`tracking_capacity` while it is full. One /48 holds 65,536 /64s, so a single
allocation rotating addresses could keep the table full and lock every other
visitor out.

**Decision.** Once the anonymous rate table or the per-user concurrency table
tracks `DataAPIAnonymousCoarseSubjects` (three quarters of the cap) subjects,
new anonymous IPv6 requests are keyed by /48 instead of /64. Below that,
keys stay /64. IPv4 keys are unchanged. The bound lives in `pkg/api/limits.go`
and is mirrored in `config.mjs` under the parity test.

**Consequences.** Under pressure, legitimate visitors who share a /48 with an
abusive one share its budget until the table drains. An attacker with many
/48s can still fill the table; the edge WAF remains the defence against that.
