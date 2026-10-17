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
normalized version 4 snapshots; the current CLI also accepts version 1, 2 and 3 baselines.

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

### Per-user request rates

User rate limits are opt-in. Set `DATA_API_RATE_LIMIT_PER_SECOND` and/or
`DATA_API_RATE_LIMIT_BURST` in the deployment environment; either setting enables
the policy. The omitted setting defaults to 10 requests/second or a burst of 20.
Both accept integers from 1 to 1,000. Remove both to disable, and fresh-restart
after changing settings. Invalid values fail startup.

Each verified JWT subject has a token bucket shared across REST, RPC and
authenticated metadata routes. Token rotation does not reset it. Rejections
return `429 data_api_rate_limited`, `Retry-After: 1`, a request ID and a fixed
reason before any SQL dispatch, including refresh/readiness probes. Public health
and CORS preflight bypass the limiter. The existing admission queue still bounds
JWT verification; this does not guarantee per-user queue fairness.

Tracking holds at most 4,096 hashed subjects. Fully refilled buckets are swept
lazily at most once per second. If tracking is full, new subjects receive 429
with reason `tracking_capacity`; depleted buckets are retained and existing users
can still use their own credit. Logs contain only policy, retry delay and fixed
reason. CLI diagnostics count `failures.rate_limited`; health records a recent
finding. SDK classification returns `rate_limited` / `wait` without automatic
replay. Only the gateway's stable code establishes pre-SQL rejection; generic
HTTP 429 does not establish rollback. Budgets are per runtime, reset on a cold
process start, and are not shared across replicas.

### Total request deadline

Authenticated requests have a total 15-second deadline after admission, including
verification, refresh, signing, engine work and response streaming. Queue wait
retains its separate one-second bound. Continuous chunks cannot extend the total
budget. Expiry returns `504 request_deadline_exceeded` before headers or closes
an incomplete streamed response. Async work is checked again before dispatch;
unfinished verification/control reads keep their slot until settlement.

Deadline logs contain fixed phases, elapsed time and engine HTTP dispatch state;
CLI diagnostics aggregate these observations. SQL may continue after HTTP closes:
reconcile uncertain writes before retry. The updated SDK recognizes the deadline
code without adding mutation retries. See `docs/data-api.md` for exact semantics.

### Response byte limit

Normal REST/RPC bodies, including engine error bodies, are limited to 16 MiB
(16,777,216 bytes); exactly that size is allowed. The gateway streams with
backpressure and counts bytes, without buffering the full result. An oversized
engine `Content-Length` returns `502 response_too_large` before gateway headers.
Otherwise overflow closes the incomplete stream; an already-sent status can
remain 200. HEAD and bodyless responses do not count representation lengths.
The separate 1 MiB request/schema bounds and 1,000-row cap still apply.

Limit failures log only fixed source and byte-bound metadata. CLI diagnostics
counts `failures.response_too_large`, including aborted 200 streams; request-ID
lookup retains `response_limit`. Health reports these as recent findings. The
SDK classifies the problem code as `response_too_large` with recovery `reconcile`;
interrupted bodies can instead have status 0 and `no_response`. Capture
`X-Request-Id` with `onResponse` to correlate those failures. Writes may already
have committed: reconcile before retrying. Reduce projections or page sizes for
future reads, and keep RPC results small. No mutation retries are added.

### Health checks

`/livez` is a public, uncached gateway-process check with no SQL, engine or JWT
work. Keep deployment health checks on `/healthz`: it preserves SQL readiness
and adds versioned engine TCP reachability, SQL readiness, database-connectivity
evidence, refresh state and current admission counts. Failed SQL checks leave
database connectivity unknown; they can also reflect a saturated pool. A known
refresh pause remains separate from an unavailable reader. Probes have a bounded
two-second observation deadline and share in-flight work without adding SQL pools.

`/healthz` also includes a `pool` observation with `checked_at`. A bounded private
engine metrics read reports capacity, existing idle (`available`) connections,
pending acquisitions (`waiting`) and cumulative acquisition `timeouts_total`.
Available is not unused configured capacity in the lazy pool, so subtraction
cannot derive active connections. Waiters can include readiness probes and are
separate from the HTTP admission queue. The timeout counter can reset and is
not a recent-failure count or a current health failure.

The pinned admin handler checks SQL twice before metrics when its DB listener
is disabled. Saturation can therefore make counts unknown. Two-second callers
share pending work until response or the existing 15-second hard HTTP cap; SQL
work can outlive HTTP cancellation. Reads reuse the engine pool and the 8 KiB
health-response cap; no completed readings are cached. Invalid/unavailable
metrics yield `state: "unknown"` and a fixed reason with no counts. They do not
change SQL readiness or the deployment HTTP status. The CLI reports pool waiters
as a current finding, and missing/invalid observations as partial coverage while
preserving valid readiness. These are per-engine counts, not SQL timings or a
database-wide connection budget. The metrics endpoint and raw bodies stay private.

`gregale data-api health NAME [--since 15m]` combines these current checks with
safe retained request diagnostics. Partial log coverage stays explicit; recent
failures are separate from current health. Production startup failures and engine
exit still stop the runtime. See `docs/data-api.md` for field and exit semantics.

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

Admitted requests also log fixed `timings` phases: `verification` (JWT and gateway
authorization/contract checks), `refresh` (barrier), `readiness` (schema probes),
`signing` (engine JWT), `upstream` (HTTP dispatch until engine response headers)
and `streaming` (headers until finish/close). Each entered phase has
`duration_ms` and `complete`; timeout/cancellation leaves the active phase
incomplete. Queue wait stays separate. Engine wait includes transport, request
upload, pool and SQL work, without isolating SQL execution or pool wait.

`gregale data-api diagnostics NAME` reports phase coverage and completed-only
p50/p95/p99; interrupted elapsed lower bounds are separate in JSON. Missing
phases are absent, and legacy/bypassed requests count as `without_timings`.
With `DATA_API_QUERY_SHAPES=1`, use `--sort p95` or `--sort errors` to rank query
shapes by total gateway-request latency (including aborts) or failure count.
The default remains request count. No raw SQL, tokens or payloads are logged.

For coordinated schema updates, the owner can install the private barrier with
`MIGRATION_DATABASE_URL` and `node refresh.mjs install BINDING_ROLE`, then deploy
all serving runtimes with `DATA_API_COORDINATED_REFRESH=1`. The same tool provides
`pause`/`resume`; resume needs the CLI-provided verified fingerprint. Keep owner
credentials and the private refresh token out of the deployed/public client.
See `docs/data-api.md` for the complete coordinated sync and recovery workflow.
