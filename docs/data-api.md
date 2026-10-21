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

## Runtime admission and overload

`gregale data-api health NAME` now also reads account-scoped database connection
planning reports. The report's `connection_budget` is separate from current SQL
readiness and per-engine pool metrics. `unknown` capacity or incomplete coverage
makes the report partial and the command exits nonzero; it does not declare a
database outage. Older management APIs and keys without `managed-postgres:read`
also leave this section unknown. Apps with no visible managed Data API binding
report `not_applicable`.

`GET /v1/postgres/databases/{id}/connection-budget` reads one database snapshot;
`GET /v1/apps/{slug}/data-api/connection-budget` lists its shared Data API database
reports, including demand from other bindings and scopes in the same account.
Both require managed PostgreSQL read permission and return `Cache-Control:
no-store`. No provider calls, SQL probes, credential delivery or admission changes
occur. Different database snapshots are independent.

The planning model counts each Data API runtime at the connections it reports
(its [engine pool](#engine-pool-size), including the reserved reader when
coordinated refresh splits the pool). Unreported and planned runtimes count at
the largest pool reported for the same binding, or two when none is reported,
and Auth runtimes at four each. It retains nonterminal resident states and parked instances,
deduplicates app/scope/access consumers, and plans for the greater of observed
resident-plus-parked instances or effective concurrency plus one overlap slot
when rotation/deployment overlap is observed. Existing replacements are not
added a second time. One database control slot and a migration slot when such a
binding exists are planning headroom, not reserved connections or a measured
maximum. Unknown client pools, auxiliary workloads/tasks, migration uncertainty,
stale node heartbeats, incomplete inventory and unverified pool versions remain
explicit reasons. Counts and estimates are not live SQL sessions or available
database capacity.

Current adapters do not provide verified tenant-usable capacity after other
reservations, so their reports remain `unknown`. A future qualified cached
observation must match the database, backend placement and generation and be
fresh before `within_budget` or `over_budget` can be reported. Compute class,
`max_connections` alone and successful SQL readiness cannot supply that evidence.
Reports expire within 30 seconds, cap database/binding inventory at 100/1,024,
and omit credentials, provider identities, node addresses and consumer app names.

Each Data API runtime admits at most two requests to authenticated endpoints
at a time and queues at most eight more in arrival order. Coordinated refresh reserves one
database connection for its control reader and admits one request at a time.
These bounds cover JWT verification, REST/RPC and authenticated schema/OpenAPI
requests through response completion. Health checks and CORS preflight bypass
the queue; a healthy engine does not imply spare request capacity.

A full queue or a wait of one second returns HTTP 503 with stable code
`data_api_overloaded`, a gateway request ID and `Retry-After: 1`. Allowed browser
origins can read `Retry-After`. These rejections happen before authentication or
PostgREST dispatch; they do not execute the queued mutation. Queued disconnects
are removed, and authentication and coordinated-refresh checks run after admission.
Disconnecting during verification retains the active slot until verification
settles. Active requests also have the elapsed-time deadline described below.

Admission is local to each runtime, not an account rate limit, a global database
connection budget or a per-user fairness guarantee. A request already dispatched
to PostgREST can commit after a timeout or disconnect; reconcile uncertain writes
using application receipts before retrying. The runtime and SDK add no automatic
mutation retries. `classifyDataError` recognizes the overload code as `unavailable`,
including errors returned by `throwOnError()`.

Request logs add safe `admission` metadata: outcome, queue wait in milliseconds,
and active/queued counts at the admission decision. `data-api diagnostics`
summarizes observed admissions, full queues, expired waits, queued cancellations,
wait p50/p95/p99 and observed active/queued peaks. `failures.overloaded` counts
overload responses separately from query timeouts and engine unavailability.
Older logs and routes bypassing admission have no admission metadata; zero
observations give null wait percentiles. These are retained-log samples, not
durable counters, SQL execution timing or measurements of SQL pool wait.

## Engine pool size

Each Data API instance runs its requests on a pool of engine connections: two by
default. A busy API that answers `503 data_api_overloaded` while the database
has room can use more:

```sh
gregale data-api configure shop-data --pool 6
```

The pool takes 1 to 8 connections, and `--pool default` returns to two.
Requests in flight per instance follow it, and so do per-user concurrency
limits and the pool metrics in `data-api health`. The queue stays at eight
waiters. Data API bindings connect through the provider's connection pooler,
so a larger pool adds pooled client sessions rather than PostgreSQL server
connections. Watch `data-api health`'s connection budget when you raise it.
Coordinated refresh keeps one connection of the pool for its reader, so it
needs a pool of at least two. Keep `DATA_API_USER_CONCURRENCY` at or below
the pool, or the runtime refuses to start.

### Server-key limits

Server keys skip the per-user limits, so they have their own:

- **Concurrency.** Server-key requests together hold at most all but one of
  the pool's slots, so signed-in users and visitors always keep one.
  `--server-key-concurrency N` changes that from 1 to 8; a value above the
  pool acts as the pool. A request past the limit answers
  `429 data_api_concurrency_limited` with `reason: "server_key"` and
  `Retry-After: 1`. It is refused at once rather than queued, so backends
  should retry.
- **Rate.** `--server-key-rate-limit 50` (or `50,burst=100`) adds a token
  bucket per key slot, so each of the two keys has its own budget. A request
  past it answers `429 data_api_rate_limited` with `reason: "server_key"`.
  `--server-key-rate-limit off` removes it.

`data-api diagnostics` counts both refusals under their codes and shows the
reason, never the key.

## Per-user concurrent request limits

Set `DATA_API_USER_CONCURRENCY=1` in the deployment environment to limit each
verified user to one concurrent authenticated request per runtime. The setting
is optional and absent means disabled. It accepts decimal integers from one to
the serving admission capacity: the [engine pool](#engine-pool-size) (two by
default), less one with coordinated refresh.
Invalid settings fail startup. Rebuild from the updated runtime template and
fresh-restart after changing deployment settings.

The policy covers REST, RPC, schema and OpenAPI routes. JWTs with the same verified
`sub` share slots across rotation; caller headers and policy-like JWT claims cannot
override it. Global admission still bounds verification and the queue, and rate
checks run before user concurrency checks. Excess calls return
`429 data_api_concurrency_limited` before refresh, readiness or engine SQL, with
`Retry-After`, request ID, fixed reason `subject` or `tracking_capacity`, configured
`max_concurrent`, and `active`/`cooling_down` counts. Public health and OPTIONS bypass
this policy. `Retry-After` is advisory, normally one second for active work and up
to 15 seconds for cooldown. Other limits can reject a call first.

A slot remains occupied until both the asynchronous handler and downstream
response settle. Client cancellation cannot discard pending handler work. An
engine/control completion acknowledgement permits release when downstream work
also finishes; a still-streaming response keeps its slot. If dispatched work has
no confirmed completion when handler and downstream settle, retain a slot in
cooldown for a full 15 seconds from that point. A late genuine acknowledgement
can release its own cooldown, without affecting a replacement request.

These are HTTP admission counts, not SQL sessions. SQL can outlive a timeout,
disconnect or cooldown. A cooldown can also reject requests after SQL actually
finished if the gateway never received its acknowledgement. The limiter stores
at most 4,096 subject digests and refuses new users at that bound; active work is
never evicted. Lazy cleanup opens capacity after eligible cooldowns expire, without
adding SQL probes or pools. There is no distributed quota or per-user queue, and
the existing global FIFO queue does not guarantee fairness. Process restart clears
the limiter; other runtime instances have independent budgets.

Logs add `concurrency_limit` only to rejections, containing the fixed reason,
policy, active/cooling counts and retry seconds. Subjects, digests, JWTs and SQL
values are excluded. `data-api diagnostics` counts
`failures.concurrency_limited` separately from rates, overload and unavailability;
`--request-id` retains the safe metadata. Health reports
`user_concurrency_limits_observed` as a recent finding, without changing current
readiness. SDK `classifyDataError` returns `concurrency_limited` / `wait`; it adds
no automatic retries. Reconcile earlier uncertain writes independently.
See [ADR-1732](adr/1732-data-api-user-concurrency-limits.md).

## Database lock waits

New managed Data API SQL logins default to a **5-second `lock_timeout`** alongside
the existing **15-second `statement_timeout`**. Local development uses the same
defaults. This limits each PostgreSQL lock acquisition, including row, table and
advisory locks; it is not a five-second query, pool-wait or transaction deadline.
Uncontended SQL can run longer, subject to the statement and HTTP deadlines.

An uncaught lock timeout returns the engine's original SQLSTATE `55P03`;
`classifyDataError` reports `lock_conflict` / `wait`. A surfaced PostgreSQL
`40P01` is classified as `deadlock` / `wait`. `55P03` can also mean NOWAIT
lock refusal, so it does not prove a timeout. Generic HTTP failures and message
text do not identify database contention. The SDK adds no automatic retries.
Pinned PostgREST 14.3 uses Hasql transaction retries for deadlocks and
serialization failures; a deadlock can therefore resolve inside one HTTP request
and return success. This existing engine behavior is separate from SDK retries.

The failed request's PostgreSQL transaction rolls back, including earlier
transactional RPC writes. Sequence increments and external effects are not
rolled back. A failure also cannot establish the outcome of an earlier attempt.
Wait, review the operation and use its existing idempotency/receipt contract
before explicit replay. The starter keeps the same receipt key for outcome
recovery. A disconnect or missing response still leaves an uncertain outcome.

For an existing binding, use the normal [credential rotation](managed-postgres.md)
workflow to create
a login with the new defaults, then ensure serving runtimes start fresh SQL
sessions. Credential issuance/reconciliation can also upgrade the exact legacy
role policy atomically without changing its password. Unexpected settings,
memberships, markers or privilege drift are refused. Cached credential delivery
need not invoke reconciliation, and changing role defaults does not affect open
sessions. A runtime rebuild alone does not upgrade a database role.

```sh
gregale postgres bindings rotate BINDING_ID --wait
```

Rotation refreshes serving runtimes through the existing binding lifecycle;
`--wait` polls until `rotation_pending` clears.

Update generated migration/RPC permission tooling before using new roles: the
updated tool accepts both exact policy versions and keeps `ready` scoped to ACL
checks. It does not change timeouts. Database-specific settings or trusted RPC
functions can override login defaults or catch SQL errors; these defaults do not
guarantee cancelled SQL stops. Disposable provider qualification verifies effective
timeouts on its new connection, independently of deployed pools.
See [ADR-1733](adr/1733-data-api-lock-contention-policy.md).

## SQL deadlines across transaction retries

`DATA_API_SQL_DEADLINE=1` optionally carries the original admitted request's
15-second budget into PostgreSQL transaction retries. Pinned PostgREST 14.3 can
retry deadlocks and serialization failures internally; a statement timeout alone
can restart on each attempt. The hook checks one fixed expiry on each attempt,
caps subsequent statement/lock timeouts to the remaining budget, and preserves
stricter existing settings. Authentication, signing, refresh waits and engine pool
waits consume that budget. There is no extra serving connection pool or per-request
SQL probe. Disabled deployments retain their current hook selection.
Opt-in disables PostgREST in-database engine configuration loading so a role/database
setting cannot replace the required hook. Existing PostgreSQL login defaults remain;
review deployments that rely on PostgREST's in-database engine/role configuration.

Rebuild with the updated runtime template, keeping the setting absent initially.
Use the owner-authorized migration task/connection to install and check the hooks
for the exact serving binding login:

```sh
node data-api-runtime/sql-deadline.mjs install YOUR_BINDING_LOGIN_ROLE
node data-api-runtime/sql-deadline.mjs check YOUR_BINDING_LOGIN_ROLE
```

These commands use `MIGRATION_DATABASE_URL`. The installer verifies serving-role
permissions through the catalog and needs no membership allowing it to assume
the serving login. For a coordinated-refresh deployment,
install its existing barrier first and run both commands with
`DATA_API_COORDINATED_REFRESH=1`. Then enable `DATA_API_SQL_DEADLINE=1` and start
fresh runtimes. Startup checks the pinned engine and installed hook bodies,
security attributes and grants before serving; it never installs or repairs them.
The `check` command is read only. By default each new/rotated login requires
authorized hook grants before adoption.

For managed Neon `data_api` bindings in a database without coordinated SQL refresh
gates, the managed schema owner can explicitly approve automatic deadline grants
for subsequent credential generations:

```sh
node data-api-runtime/sql-deadline.mjs enroll YOUR_BINDING_LOGIN_ROLE YOUR_BINDING_UUID
node data-api-runtime/sql-deadline.mjs check-enrollment YOUR_BINDING_LOGIN_ROLE YOUR_BINDING_UUID
```

Use the existing migration connection, whose effective role is the stable managed
schema owner. Install/check the hooks first. Enrollment records the binding UUID,
provider resource scope and version-one body hashes in a private owner-only table.
It neither installs hooks nor turns on the runtime setting. Each candidate login
receives only schema usage and execution of the two matching deadline hooks,
inside credential preparation and before the app secret is replaced. Ownership,
body, security-attribute or policy-table drift blocks publication and rolls back
preparation; the previous credential stays available. Repair the owner setup
and retry the existing rotation rather than creating another generation. Existing
rolling runtime refresh and retirement still determine when the previous login
is revoked. Arbitrary RPC grants are not copied; retain their permission workflow.

`check-enrollment` is read only. Repeated enrollment preserves a matching approval
and rejects incompatible versions/hashes. `unenroll BINDING_ROLE BINDING_UUID`
removes future automation without revoking existing hook grants. Binding or
resource scopes without an approval retain manual setup. Keep coordinated SQL
refresh grants and gate setup manual: any `gregale_data_api.refresh_gate` in this
database blocks automated preparation for enrolled bindings. Unenrollment remains
available after a gate is added. This prevents adopting a candidate that bypasses
the predecessor's pause/fingerprint contract. See [ADR-1735](adr/1735-data-api-deadline-grant-rotation.md).

The coordinated enrollment below replaces that manual rotation boundary for
supported managed bindings.

The gateway overwrites the reserved `gregale_sql_deadline` JWT claim and strips
it when disabled. Caller headers and claims cannot extend the deadline or select
refresh behavior. With coordinated refresh, the SQL hook checks time before the
existing barrier and recalculates after it; pause, fingerprint checks and pool
reservation retain their existing behavior.

If the hook observes expiry, it returns HTTP 504 with
`data_api_sql_deadline_exceeded`, classified by the SDK as `timeout` / `reconcile`.
Malformed/missing/version-mismatched deadline claims fail closed with HTTP 500
and `data_api_sql_deadline_invalid`. PostgreSQL can instead return `57014` when
the main statement timer fires, or `55P03` for a lock wait; the gateway HTTP timer
may also win the response race. Keep existing cancellation/lock guidance and
reconcile uncertain writes before explicit replay. No SDK retries are added.

This contract assumes stable, reasonably synchronized gateway/database clocks.
An ahead database clock shortens the budget; a substantially behind clock rejects
the future deadline. Backward clock steps can extend elapsed work. Pool acquisition
is not cancelled by this hook: an expired queued request is refused when its hook
runs. Trusted SQL functions can override timeouts or catch errors, and external
effects/sequences are not rolled back. HTTP abort still does not prove SQL stopped.
See [ADR-1734](adr/1734-data-api-sql-retry-deadlines.md) for the boundaries and tests.

## Credential rotation with coordinated SQL refresh

Managed Neon `data_api` bindings using the `api` schema can explicitly enroll in
rotation support for coordinated refresh. Rebuild from the updated runtime
template. Install the existing refresh barrier first and keep
`DATA_API_COORDINATED_REFRESH=1` on serving runtimes. Use the migration connection
whose effective role is the stable managed schema owner:

```sh
node data-api-runtime/refresh-rotation.mjs enroll YOUR_BINDING_LOGIN_ROLE YOUR_BINDING_UUID
node data-api-runtime/refresh-rotation.mjs check-enrollment YOUR_BINDING_LOGIN_ROLE YOUR_BINDING_UUID
```

If the serving login has SQL deadline hook grants, install/check those hooks first
and run both enrollment commands with `DATA_API_SQL_DEADLINE=1`. The recorded
deadline mode must match on repeated enrollment. If this binding previously used
deadline-only enrollment, remove that approval with `sql-deadline.mjs unenroll`
before enrolling coordinated rotation; existing deadline grants remain intact.

Enrollment drains admissions using the old login's lock, preserves an existing
pause and its owner token, then moves protection to a shared binding gate.
Queued calls to the old hook fail closed after its legacy gate row is removed.
Both old and replacement logins subsequently honor the same pause/resume state.
Each login has its own verified schema fingerprint. Other per-login legacy
barriers continue through the upgraded hooks. Once the database uses rotation
hooks, enroll additional managed bindings with this tool; the old `install`
command refuses to overwrite them. Owner enrollment and read-only checks use
catalog privileges and do not need membership in serving logins.

Replacement preparation grants the two fixed refresh functions, optional matching
deadline functions and the source login's explicitly approved `@gregale:rpc`
functions. It compares a pinned catalog/permission witness before committing the
candidate and publishing credentials. It does not copy arbitrary grants or table
ownership. Invalid hook/table metadata, source-role identity, RPC definitions or
permissions block publication and leave current credentials available. A failed
attempt can resume the same credential generation after owner repair.

Continue using the existing `refresh.mjs pause|resume|check` commands and
coordinated sync/recovery workflow. Pause drains requests from every enrolled
generation. Resume verifies the supplied fingerprint for the requested login,
inspects every remaining member using that member's privileges, and records each
verified fingerprint and catalog witness before releasing the shared pause.
Stale runtimes receive `schema_contract_mismatch` until refreshed. The scheduler's
existing rolling credential refresh determines when the old login is revoked;
retirement removes only its mapping. The shared gate survives until the last
login is retired, including during binding deletion.

DDL or permission changes after approval can conservatively block preparation,
including unrelated catalog DDL. Use coordinated pause/verification/resume to
approve the current catalog before retrying rotation. Preparation can join an
already paused gate when its approved witness still matches; changes during a
migration keep it blocked until verification. Arbitrary owner edits outside this
workflow remain an operational coordination concern. There is no additional
serving connection pool, request probe or automatic mutation replay. See
[ADR-1667](adr/1667-data-api-coordinated-refresh-rotation.md).

### Rotation preflight and progress

Before rotating an enrolled coordinated-refresh binding, check it using the
matching owner tool and the same managed migration connection used for setup:

```sh
node data-api-runtime/refresh-rotation.mjs preflight YOUR_BINDING_LOGIN_ROLE YOUR_BINDING_UUID
```

Keep `MIGRATION_DATABASE_URL` in the local owner environment and set
`DATA_API_SQL_DEADLINE=1` when that mode is enrolled. The command checks enrollment,
protected hook versions and ACLs, source identity, deadline and RPC grants,
catalog/schema drift, managed baseline privileges and RLS policy role lists.
It returns JSON with stable check codes and repair steps. `passed`, `blocked`
and `unknown` distinguish checked conditions from missing evidence. Blocked or
unknown checks exit nonzero. It runs a read-only transaction, always rolls back,
and needs neither an application JWT nor a refresh token. An unchanged paused
gate can pass because replacement preparation preserves that pause.

For a combined CLI report, save a trusted local command in `rotation-check.json`
(paths resolve relative to the file's directory):

```json
{
  "command": ["node", "data-api-runtime/refresh-rotation.mjs", "preflight", "YOUR_BINDING_LOGIN_ROLE", "YOUR_BINDING_UUID"],
  "directory": "."
}
```

```sh
gregale --json data-api rotation preflight notes-data --binding YOUR_BINDING_UUID --owner-check rotation-check.json
gregale --json data-api rotation status notes-data --binding YOUR_BINDING_UUID
gregale --json data-api diagnostics notes-data --rotation-binding YOUR_BINDING_UUID
```

The CLI verifies that this Data API binding belongs to the app before running
the owner command. Its `ready` result requires passing owner checks, complete and
consistent inventory, a ready binding and no pending predecessor cleanup. Owner
output is bounded and validated; arbitrary stdout/stderr is not relayed. Use the
documented read-only command: the configuration is trusted executable code and
inherits your environment. The CLI cannot make an arbitrary owner command safe.
`--timeout` bounds the entire check (default two minutes, maximum one hour).

Status shows the credential generation, refresh handoff and retirement progress.
A completed refresh may still have `cleanup_pending`; `retiring` records active
managed cleanup. `no_previous_generation_pending` means the catalog records no
predecessor cleanup, including bindings that have never rotated. It is not an
independent provider revocation test. Runtime freshness compares configuration
and instance timestamps; it does not prove credential use or readiness. Inventory
comes from separate reads; conflicting or partial observations require a retry.
Hosted diagnostics includes this evidence only with `--rotation-binding` and
marks metadata failures partial. Offline `--file` analysis rejects that flag.

Repair catalog or grant drift through coordinated pause, verification and resume;
preflight never adopts it. Review policies whose `TO` list names the source SQL
login: a replacement has a different identity. Use owner-reviewed policies that
apply to replacement logins and preserve application-claim isolation. A passing
preflight remains advisory: it does not evaluate arbitrary RLS expressions or
prove future candidate privileges, runtime adoption or provider revocation.
Preparation rechecks the full witness transactionally before publication. See
[ADR-1668](adr/1668-data-api-rotation-preflight.md).

## Per-user request rate limits

Enable an optional policy through the Data API deployment environment:

```sh
DATA_API_RATE_LIMIT_PER_SECOND=10
DATA_API_RATE_LIMIT_BURST=20
```

Either variable enables the policy. If one is omitted, its default is 10 requests
per second or a burst of 20. Both must be decimal integers from 1 to 1,000;
empty, zero, fractional and out-of-range values fail startup. Remove both
variables to disable. Update the runtime and fresh-restart after changing these
deployment settings; existing templates without this feature ignore them.
These are owner-selected application limits, separate from platform plan quotas.

The gateway checks a token bucket keyed by the verified JWT `sub` after admission
and JWT/claim validation, before refresh checks, SQL readiness probes, signing
or data-engine dispatch. A new subject starts with `burst` credit; each request
uses one credit, replenished continuously at `per_second` up to that burst.
REST reads/writes, RPCs, OpenAPI and serving-contract reads share one bucket.
Rotating tokens or changing routes does not reset the same subject's budget.
Missing/invalid JWTs do not create buckets. Health probes and CORS preflight
bypass this policy. Valid subjects' subsequent permission/contract failures can
consume credit. Each request has the same cost; this is not a SQL-cost budget.

Rejection returns HTTP 429 with code `data_api_rate_limited`, `Retry-After: 1`,
`Cache-Control: no-store` and the gateway request ID. Allowed browser origins
can read the retry header. The problem includes `reason`, `per_second`, `burst`,
`retry_after_seconds`, policy `limit`, safe `observed` counts and a documentation
link. `reason: "subject"` means there is no whole request credit available;
`reason: "tracking_capacity"` means no slot is available for a new subject.
HEAD returns status/headers without a problem body. A rejected write never
reaches PostgreSQL through this gateway. No automatic replay is added.
Generated OpenAPI advertises the 429 problem shape and retry header.

Each runtime holds at most 4,096 hashed subject keys. A lazy sweep at most once
per second removes only fully refilled buckets, which can safely restart full.
When tracking fills, new subjects are rejected until slots become available;
existing depleted buckets are not evicted to make room, preventing identity
churn from resetting their budgets. Existing subjects may still use their
available credit. Cleanup adds no timers or SQL connections. Logs retain no
subjects, hashes, tokens or per-user counters: `rate_limit` contains only fixed
`reason`, `per_second`, `burst` and `retry_after_seconds`.

CLI diagnostics validate this metadata, count `failures.rate_limited` separately
from overload/unavailability, and retain the safe policy in request-ID lookup.
Unknown fields are discarded; malformed/inconsistent metadata makes coverage
partial. Old records without rate-limit observations remain readable. Health
adds `rate_limits_observed` to recent findings without changing current readiness.
The SDK recognizes the stable code and HTTP 429 as `rate_limited` with recovery
`wait`, including `throwOnError()` when the code is available. This guidance
defers a decision; it never retries. Generic 429 can come from another service
or application SQL and does not establish that a mutation was rejected before
execution. Reconcile uncertain writes before replaying them.

Budgets are local to a runtime and reset on a cold process start; snapshots may
retain in-memory state. Replicas do not share counters. The existing global
admission queue runs before authentication, so this does not provide per-user
queue fairness, protect against all verification floods, or enforce a distributed
account/tenant quota. Shared quotas and deployed load qualification remain separate.

## Total request deadline

Each admitted authenticated request has one 15-second elapsed-time budget, using
the canonical query bound in `pkg/api/limits.go`. The budget starts when the
request receives an active slot, after the separate one-second admission wait.
It includes JWT verification, coordinated-refresh checks, signing, upstream
request/body handling and response streaming. Authenticated schema and OpenAPI
requests share this budget; public health and CORS checks retain their existing
separate behavior. Continuous response chunks cannot extend the deadline.

The runtime checks monotonic elapsed time after asynchronous work and before
engine dispatch, as well as through a deadline timer. Expired verification,
refresh or signing cannot later dispatch the original request. Before response
headers, expiry returns HTTP 504 with stable code `request_deadline_exceeded`,
the gateway request ID and `Connection: close`. After headers, expiry closes the
incomplete response; a previously sent status such as 200 cannot become a 504.
HTTP work is aborted, and normal completion or client cancellation clears the
timer. Timers run when the Node event loop can execute them; this is not a
preemptive CPU execution limit.

An active verification or refresh operation retains its admission slot until it
settles, even if its HTTP response already timed out. This preserves the active
work bound; a timeout must not admit more unfinished verification work. Closing
an engine HTTP request does not prove PostgreSQL cancelled or rolled back SQL.
Dispatched writes remain uncertain and require receipt/data reconciliation before
retry. SQL statement timeouts remain independent. No automatic mutation retry is
introduced, and the new SDK classifies `request_deadline_exceeded` as `timeout`
with recovery guidance `reconcile`, including `throwOnError()` errors.

Timed-out request logs add safe `deadline` metadata: `phase`, `elapsed_ms` since
admission and `dispatched`. Fixed phases are `verification`, `refresh`, `readiness`,
`signing`, `upstream` and `streaming`. Streaming includes engine response buffering
and OpenAPI rendering. `dispatched` records an attempted data-engine HTTP request;
it is not evidence of SQL execution/commit and excludes refresh/admin SQL probes.
An upstream-phase expiry can precede dispatch and then records `dispatched: false`.
Pre-header expiry has outcome `query_timeout`; incomplete responses keep outcome
`aborted`, the already-sent status and the deadline code. No raw error, token,
SQL, query value or response body enters the metadata.

`data-api diagnostics` reports `deadlines.observed`, `deadlines.dispatched` and
counts in `deadlines.by_phase`. `failures.timeouts` includes both old inactivity
timeouts and total deadlines, including interrupted streams whose sent status was
200. Older records remain readable with no deadline-phase observations. Request
ID lookup retains the validated timeout metadata; malformed phase records make
the retained sample partial rather than publishing untrusted fields. These are
sampled HTTP observations, not SQL execution timings or rollback guarantees.

## Response byte limit

Normal REST/RPC response bodies, including engine error bodies, have a 16 MiB
(16,777,216-byte) limit from `api.DataAPIMaxResponseBytes`. Exactly that many
bytes are allowed. The gateway counts actual body bytes while streaming and
preserves downstream backpressure; it does not buffer the full result. This
limit is independent of the 1,000-row cap: a single large JSON value can exceed
it. Request bodies and successful OpenAPI/schema rendering retain their separate
1 MiB bounds. HEAD and bodyless HTTP responses do not count a representation's
advertised size, and HTTP headers are excluded from body byte accounting.

If the engine advertises an oversized `Content-Length`, the gateway rejects it
before sending headers with HTTP 502 and stable code `response_too_large`. The
problem includes `request_id`, `limit`, `observed`, `observed_is_lower_bound: true`
and a documentation link. `observed` is always the safe lower bound 16,777,217,
not a measurement of the complete result. Otherwise the gateway checks each
chunk before forwarding it. Overflow after a prefix was sent closes the
incomplete response, keeping its original sent status, which may be 200. It
cannot append a JSON problem or replace those headers. A first oversized chunk
can still return the 502 problem if headers have not been sent.

Safe logs record code `response_too_large` and `response_limit` metadata with
`source` (`content_length` or `stream`), `limit_bytes` and
`observed_at_least_bytes`. They contain no result values or exact result size.
An interrupted stream records outcome `aborted` and its sent status; a completed
502 problem records `rejected`. The streaming timing remains incomplete.
`data-api diagnostics` counts both in `failures.response_too_large`, and request-ID
lookup retains the validated metadata. `data-api health` reports
`response_limits_observed` among recent findings, separately from current readiness.
Older records remain readable; malformed metadata marks the sample partial.

The SDK classifies the problem code as `response_too_large` with recovery
`reconcile`, including errors from `throwOnError()`. An interrupted body can
instead surface as status 0 / `no_response`; a transport failure alone does not
identify its cause. Capture the header request ID with `onResponse` and look up
the gateway log. A mutation may commit before its response exceeds this limit.
Reconcile its receipt or resulting data before replaying it; a 502 does not
establish rollback. No automatic mutation retries are added. For future requests,
use smaller projections/pages and bounded RPC results. Existing apps need the
updated runtime; applications need the updated SDK for the new category.

## Guided first application

With Node.js 22+, npm, a running local Docker daemon and an approved matching
CLI/SDK bundle, create and verify a complete project with:

```sh
gregale data-api onboard --path notes --bundle /path/to/data-api-bundle.json
```

This composes the existing starter, verified SDK installer and disposable local
workflow. It generates types, compiles and tests the client, and replays CRUD,
two-user isolation and RPC scenarios. It exits after verification and removes
its disposable services. The project, artifact pin, lockfile and generated types
remain for development. Existing nonempty directories are refused.

Each stage reports its result and duration. If installation or verification
fails, fix the reported cause and use the printed commands to continue in the
retained project. Do not rerun onboarding over that project. `--json` emits
newline-delimited `onboarding_step` records; nested workflow output goes to
stderr. `local_activation: passed` means this local starter passed its checks;
it does not establish hosted deployment, production policy correctness, or a
customer activation-time target.

Follow the [first-application journey](data-api-onboarding.md) to open the
browser example, evolve a schema, investigate a failure and prepare a pilot.

## Team orders dashboard starter

For a shared-team application, use the embedded `data-api-teams` starter:

```sh
gregale data-api onboard --template data-api-teams --path team-orders --bundle /path/to/data-api-bundle.json
cd team-orders
gregale data-api dev --port 0 --check
```

Build and serve its browser client with `npm run build:browser --prefix client`
and `npm run serve --prefix client`. Database membership grants owners/admins
order writes and members shared reads. Tenant claims do not grant membership.
The typed dashboard includes status filters, UUID cursor pages, version-checked
idempotent saves and explicit same-user recovery after uncertain responses.
Owners can also add registered identity-provider IDs, edit/remove membership
with UUID revisions, transfer ownership atomically and browse access history.
The last owner is protected. Exact-key receipt recovery confirms transfers even
after the original actor loses ownership; directories and history remain
owner-only. The local collection tests tenant isolation, role escalation denial,
membership administration, expiring member/admin invitations and receipt replay.
Invitations register the verified accepting subject without manual ID provisioning.
Committed declarations can be verified with `--check-types` in CI.

The [starter README](../cmd/gregale/templates/data-api-teams/README.md) explains
owner-controlled membership provisioning, hosted adoption and receipt retention.
Seed memberships are local test fixtures. Membership changes use reviewed application RPCs. The starter adds no identity
service, invitation email delivery, automatic write retries, payments or hosted
qualification.

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
`http://localhost:5173`, `http://127.0.0.1:5173`, and the starter browser
origins `http://localhost:3030` and `http://127.0.0.1:3030`.

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

## Public reads

By default every data request needs an application JWT. To serve catalog,
pricing or other public data to visitors without a session, list the relations
anyone may read:

```sh
gregale data-api configure shop-data --public-read api.products,api.categories
```

`configure` stores the setting and requests a fresh restart (`--no-restart` skips
it). `gregale data-api create` accepts the same `--public-read` flag, and
`--no-public-read` turns the feature off again.

A request without an `Authorization` header is then anonymous, but only when it
is a `GET` or `HEAD` of a listed relation (or of a [public RPC](#public-rpcs)).
Every other unauthenticated request still returns `401 token_required`,
including other RPCs, OpenAPI, writes and unlisted relations. A present but invalid token is never treated as anonymous. Embedded
resources in `select` must also be listed: `select=*,categories(name)` works,
while `select=*,orders(*)` returns `403 public_read_not_allowed` before any SQL
runs.

The runtime refuses to start (`data_api_public_read_unsafe`) unless each listed
relation is a table with row-level security enabled or a view created with
`security_invoker`. That check proves PostgreSQL owns the decision; it does not
prove your policies are right. Anonymous requests carry no `sub` claim, so
policies keyed only on the subject return no rows to them. Grant public rows
explicitly through the gateway-owned `gregale_access` claim:

```sql
ALTER TABLE api.products ENABLE ROW LEVEL SECURITY;
CREATE POLICY public_catalog ON api.products FOR SELECT
  USING (published AND (current_setting('request.jwt.claims', true)::jsonb ->> 'gregale_access') = 'anonymous');
CREATE POLICY signed_in_catalog ON api.products FOR SELECT
  USING ((current_setting('request.jwt.claims', true)::jsonb ->> 'gregale_access') = 'user');
```

`gregale_access` is `user`, `anonymous` or `server`. The gateway sets it and
overwrites any value an application token tries to supply. Every access kind
still uses the binding's restricted SQL login.

Anonymous traffic is always rate limited per client address, using the address
the Gregale edge stamps on each request (IPv6 is grouped by /64). The limit is
your `DATA_API_RATE_LIMIT_*` policy, or 10 requests per second with a burst of
20 when you have not set one. Anonymous budgets are tracked separately from
signed-in users. Request logs and `data-api diagnostics` record the access kind,
never client addresses.

Add `"public_read": ["api.products"]` to `data-api.dev.json` to try anonymous
reads in `gregale data-api dev`; the same RLS check runs locally.

From TypeScript, create the client with `anonymous: true`. It sends no
`Authorization` header when there is no token, and `accessToken` may return
`null` for signed-out visitors. Without that option a missing token throws, so
a lost session never silently becomes anonymous access:

```ts
const visitor = createDataClient<Database>({ url: dataURL, anonymous: true }).schema('api')
const { data } = await visitor.from('products').select('id,name,categories(name)')
```

### Public RPCs

Search, featured items and other computed public data often belong in a
function rather than a table. List the functions anyone may call:

```sh
gregale data-api configure shop-data --public-rpc api.search_products
```

`gregale data-api create` accepts the same `--public-rpc` flag, and
`--no-public-rpc` turns it off. Public RPCs work with or without public reads.

Anonymous callers must use `GET` (or `HEAD`) and pass arguments as query
parameters:

```sh
curl 'https://shop-data.example.com/rest/v1/rpc/search_products?q=lamp&limit=20'
```

```ts
const { data } = await visitor.rpc('search_products', { q }, { get: true })
```

An anonymous `POST` to a public function returns
`405 public_rpc_requires_get` with `Allow: GET, HEAD`. PostgREST runs `GET`
calls in a read-only transaction, so a public call cannot write, even through
a `VOLATILE` function that a `STABLE` one calls. Signed-in users and server keys
may call the same function by `GET` or `POST`. Other approved functions stay
`POST`-only and need a token.

The runtime refuses to start (`data_api_public_rpc_unsafe`) unless each listed
function is an approved RPC (annotated `@gregale:rpc`, one signature, security
invoker) declared `STABLE` or `IMMUTABLE`:

```sql
CREATE FUNCTION api.search_products(q text) RETURNS SETOF api.products
  LANGUAGE sql STABLE AS $$
    SELECT * FROM api.products WHERE name ILIKE '%' || q || '%'
  $$;
COMMENT ON FUNCTION api.search_products(text) IS '@gregale:rpc';
```

The function runs as the binding login with `gregale_access` set to
`anonymous`, so row-level security on every table it reads applies to the
caller. It can still read whatever that login may read in tables without RLS;
reviewing the function body is part of making it public. Embedded resources in
`select` must be listed in `--public-read`, as for relations.

Public RPC calls share the anonymous rate limit with public reads. Add
`"public_rpc": ["api.search_products"]` to `data-api.dev.json` to try them in
`gregale data-api dev`, where the same startup check runs.

### Cache public reads at the edge

Public rows are the same for every visitor, so the Gregale edge can serve them
without waking the API or querying PostgreSQL:

```sh
gregale data-api configure shop-data --public-read-cache 30s
```

This stores `DATA_API_PUBLIC_READ_MAX_AGE` and creates one gateway cache rule,
`data-api-public-read-cache`, for `GET /rest/v1/*` on the app's canonical host.
The maximum is one hour. The same setting covers public RPC calls.
`--public-read-cache off` removes the setting and the rule, and so does turning
off the last of public reads and public RPCs.

The rule covers the whole path, but only responses the runtime marks as
`Cache-Control: public` are stored. Those are successful (`200`) anonymous
reads of relations and public RPC calls in the default schema, using the default JSON
representation: no `Range`, `Range-Unit`, `Prefer`, `Cookie` or other-schema
`Accept-Profile` header, and an `Accept` of `application/json`, `text/plain` or
`*/*`. Every other response stays `no-store`. Signed-in and server-key requests
always bypass the edge cache, because they carry `Authorization`.

A cached response is shared across origins, so with caching on, anonymous
responses send `Access-Control-Allow-Origin: *` instead of varying on `Origin`.
Any website can then read your public rows, which anyone could already fetch
directly. Rate limits apply only to requests that reach the API.

The cache key is the method, host, path and query string. A client that sends a
representation header for a URL that is already cached gets the cached JSON
instead. Page with `limit` and `offset` query parameters, and keep exact counts,
CSV and other schemas on signed-in requests.

Changes appear once the max age passes. To show them sooner, purge by tag. Each
cached response is tagged `data-api` plus `data-api:SCHEMA.RELATION` for every
relation it contains, embedded ones included:

```sh
gregale cache purge shop-data --tag data-api:api.products   # one table
gregale cache purge shop-data --tag data-api                # every public read
```

A public RPC response is tagged `data-api-rpc:SCHEMA.FUNCTION` plus its
embedded relations. The gateway cannot see which tables a function reads, so
purging a table's tag does not purge function results built from it. Purge
`data-api-rpc:api.search_products` or `data-api` instead, or keep the max age
short.

Deployments, including single-app migrations, and rule changes also invalidate
cached entries. In a two-app project, purge `data-api` after migrating. While
the API is unreachable, the edge may serve stale entries for up to five minutes.

## Server keys for trusted backends

Backends, jobs and webhooks can call the same API with a server key instead of
a user JWT:

```sh
gregale data-api server-key create shop-data
```

The CLI generates the key locally and prints it once. Only its SHA-256 hash is
stored, as a sealed app secret, in one of two slots (`a` and `b`), and the
runtime restarts fresh to load it. Send the key as `Authorization: Bearer
gdsk_…` from server-side code only. Requests that also carry an `Origin` header
are rejected with `403 server_key_browser_forbidden`, so browser code cannot use
a key by accident.

Server keys do not bypass row-level security; the serving login cannot. A
request matches policies that grant `gregale_access = 'server'`:

```sql
CREATE POLICY backend_orders ON api.orders
  USING ((current_setting('request.jwt.claims', true)::jsonb ->> 'gregale_access') = 'server');
```

To act for one user, add `X-Gregale-Subject: USER_ID` (1–255 characters). The
gateway sets `sub`, so your ordinary user policies apply. Server-key requests
skip per-user rate limits and per-user concurrency, but not HTTP admission or
the 15-second deadline.

Rotate without downtime: create a second key (it takes the free slot), deploy it
to your backends, then revoke the old slot. Logs and diagnostics show which slot
each request used (`server_key_slot`), so you can confirm the old key is idle
first.

```sh
gregale data-api server-key list shop-data
gregale data-api server-key create shop-data            # fills the free slot
gregale data-api server-key revoke shop-data --slot a   # after backends moved
```

With both slots in use, `create` refuses unless you pass `--replace-oldest`.
`revoke --slot all` removes every key.

### Restrict what a server key can do

A key created without permissions can do everything your RLS policies allow for
`server`. Give a key only the access its backend needs:

```sh
gregale data-api server-key create shop-data \
  --read-only --relations api.orders,api.customers --no-subjects
gregale data-api server-key create shop-data \
  --relations api.orders --rpcs api.ship_order
```

- `--read-only` allows only `GET` and `HEAD`. A read-only key can call an RPC
  only when you list it in `--rpcs`, because a function may write.
- `--relations` lists the tables and views the key may use, including every
  relation embedded through `select`.
- `--rpcs` lists the approved functions the key may call.
- `--no-subjects` refuses `X-Gregale-Subject`, so the key cannot act for a user.
- A key with allowlists cannot read the full OpenAPI catalog. The
  fingerprint-only serving contract stays readable.

Anything outside a key's permissions returns `403 server_key_permission_denied`
before any SQL runs. RLS still applies to whatever the permissions allow.
Permissions belong to the slot: the CLI stores them before the key's hash and
removes them after it, so a key never has another key's permissions or none at
all. `server-key list` marks restricted slots, and request logs show
`server_key_restricted`. To change a key's permissions, create a new key with
the new flags and revoke the old slot.

## Usage and monthly caps

Each Data API reports its usage to Gregale: requests from signed-in users,
anonymous visitors and server keys, rejected requests (4xx), errors (5xx),
rows returned and response bytes, per UTC hour.

```sh
gregale data-api usage shop-data              # last 7 days, per UTC day
gregale data-api usage shop-data --since 24h  # per hour
```

The output also shows this month's totals. Use `--json` for the raw numbers;
the dashboard and SDKs read the same `GET /v1/apps/{slug}/data-api/usage`.

`gregale data-api create` turns reporting on. For a Data API created earlier,
run `gregale data-api configure shop-data --usage-reporting on`. The runtime
reports with its own short-lived workload identity, never your keys. These
counts are usage telemetry for you, not your bill: a Data API is billed like
any other app.

### Monthly caps

Caps stop a popular or abused public page from sending unbounded traffic to
your database:

```sh
gregale data-api configure shop-data --monthly-caps requests=5000000,anonymous=1000000
gregale data-api configure shop-data --monthly-caps off
```

`requests` counts every admitted request this UTC month; `anonymous` counts
only requests without a token. Once a cap is reached, the API answers
`429 data_api_monthly_cap_reached` with the cap, its limit and the observed
count, and `Retry-After` set to the start of the next UTC month. A reached
anonymous cap refuses only anonymous requests, so signed-in users and server
keys keep working. Caps turn usage reporting on.

Caps are enforced from totals the runtime learns every 15 seconds while it
serves traffic, so several instances can each go slightly past a cap before
they hear about it. `data-api diagnostics` counts requests refused by caps.
Cap values are stored as app secrets and are not shown again; set them again
to change them.

## Live reads

A live read keeps a Data API read current: the client receives the rows, then
each change as writes commit. Row-level security decides every row with the
same claims a REST read gets.

```sh
gregale data-api live enable shop-data
gregale data-api live status shop-data   # the same report, changing nothing
```

`enable` sets up only what is missing:

- live queries on the Data API's database;
- usage reporting, which also tells realtime how this Data API verifies user
  tokens, which relations are public and the hashes of its server keys (never
  the keys). The API restarts to report them;
- a realtime endpoint on the Data API app. Its connections carry live reads
  only and never call the app.

It prints the WebSocket URL and the database ID that clients use with
[`@gregale/data/live`](../sdk/data/README.md#live-reads):

```ts
const connection = new LiveConnection({ connect: liveSocket(LIVE_URL) })
const feed = connection.query({ database: DATABASE_ID, token: () => session.accessToken,
  ...liveRead(db.from('messages').select('id,body').eq('channel_id', 7)) })
```

Each read brings its own credential, checked like a REST request:

- **A signed-in user's token** is verified with the Data API's issuer, JWKS
  and audience.
- **`anonymous: true`** reads only relations in the
  [public-read allowlist](#public-reads), embedded relations included.
- **A server key** stays within its [permissions](#restrict-what-a-server-key-can-do)
  and is refused on connections opened from a browser.

Open reads check their credential again every hour, so a revoked key or a
narrowed allowlist reaches them within the hour. Live connections count
against your realtime connection limits. If live queries are not yet
available to your account, `enable` says so and changes nothing.

## File storage

Files are rows: a table `storage_objects` in your API's schema records every
stored file, and your row-level security policies on it decide who may
upload, download and remove each one. The same tokens, public-read allowlist
and server keys apply as for REST.

1. Add the table with a migration. The printed SQL creates it with RLS and a
   guard trigger, and includes example policies to adapt:

   ```sh
   gregale data-api storage sql > migrations/0005_storage.sql
   ```

   ```sql
   -- Signed-in users manage their own files in "uploads", up to 10 MiB each.
   CREATE POLICY own_uploads ON api.storage_objects
     USING (bucket = 'uploads' AND owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
     WITH CHECK (bucket = 'uploads' AND owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub' AND size <= 10485760);
   ```

2. Bind a bucket and turn storage on:

   ```sh
   gregale data-api storage enable shop-data --bucket uploads
   ```

   This creates the bucket if needed, binds it to the Data API app with read
   and write access, and restarts the API. In storage routes a bucket goes by
   its name in lower case with other characters as `_` (`user-files` becomes
   `user_files`). `storage status` lists them; `storage disable` turns storage
   off and keeps the buckets and their files.

3. Use it from the browser or a backend:

   ```ts
   import { createStorageClient } from '@gregale/data'

   const files = createStorageClient({ url: 'https://shop-data.gregale.dev', accessToken: () => session.accessToken }).from('uploads')
   await files.upload(`${userId}/avatar.png`, file)            // a Blob brings its type
   const image = await (await files.download(`${userId}/avatar.png`)).blob()
   const { url } = await files.signedUrl(`${userId}/avatar.png`) // 60 seconds, no token, for <img src>
   await files.remove(`${userId}/avatar.png`)
   ```

Each route asks PostgreSQL first, as the caller:

| Route | Your policies decide |
|---|---|
| `PUT /storage/v1/object/{bucket}/{name}` | INSERT, or UPDATE when replacing (`WITH CHECK` sees `size` and `content_type`) |
| `GET`/`HEAD /storage/v1/object/{bucket}/{name}` | SELECT |
| `POST /storage/v1/sign/{bucket}/{name}` | SELECT |
| `DELETE /storage/v1/object/{bucket}/{name}` | DELETE |

- **Refusals and missing files.** A refused upload answers
  `403 storage_forbidden`. A file the caller may not see answers
  `404 storage_not_found`, exactly like a missing one.
- **Owners.** `owner` is the caller's `sub`. A server key acting for a user
  (`X-Gregale-Subject`) uploads as that user. Replacing a file keeps its owner
  and creation time.
- **List files with queries.** Query `storage_objects` like any table: REST,
  generated types and [live reads](#live-reads) all follow your policies.
  Only the storage routes can change its rows; REST writes to it are refused.
- **Visitors.** They can download and sign files when
  `api.storage_objects` is in the [public-read allowlist](#public-reads);
  policies still decide which files. Visitors cannot upload or remove files.
- **Sizes.** Uploads need `Content-Length` and are at most 25 MiB by default
  (`--max-object-bytes`, up to 100 MiB). Files stream through the Data API,
  four transfers at a time per instance, each within two minutes.
- **Safe serving.** Downloads are served with `Content-Security-Policy:
  sandbox` and `nosniff`, so an uploaded page cannot run as your API's
  origin.
- **Before the migration.** Until `storage_objects` exists with RLS and its
  guard trigger, storage routes answer `503 storage_unavailable` and the rest
  of the API keeps serving.

## Aggregates

Aggregate queries are off by default. Turn them on with:

```sh
gregale data-api configure shop-data --aggregates on
```

Then a read can group and total rows, for example
`GET /rest/v1/orders?select=status,count()` or
`select=customer_id,total:amount.sum()`. Rows are grouped by the
non-aggregated columns you select, and RLS applies before aggregation, so each
caller totals only the rows they can read.

Aggregates require the [SQL deadline](#sql-deadlines-across-transaction-retries).
An HTTP timeout cannot stop an expensive aggregate inside PostgreSQL, but the
per-request `statement_timeout` can. Install the deadline functions first:
`configure --aggregates on` enables `DATA_API_SQL_DEADLINE=1`, and a runtime
whose hook is missing refuses to start. Row, response-size, rate-limit and
admission bounds are unchanged. Generated row types describe table columns, so
type aggregate results in your client code. `--aggregates off` turns them off
and leaves the SQL deadline in place.

## Single-app projects

A Data API normally uses two apps: the API and a migration app whose release
task applies your SQL. A single-app project runs both in one app, which fits
plans with one deployed app:

```sh
gregale data-api create shop-data --database shop-db \
  --issuer https://identity.example.com/ \
  --jwks-url https://identity.example.com/.well-known/jwks.json \
  --audience shop --migrations migrations
```

The app holds two bindings to the same database:

- `DATABASE_URL` uses the restricted `data_api` login and serves requests.
- `MIGRATION_DATABASE_URL` uses the `migration` login, which is delivered only
  to release tasks.

The CLI uploads the pinned runtime together with your `migrations` directory and
declares `release: node migrations/release.mjs`. Every deployment therefore
applies migrations first, and its new instances load the new schema when they
start; no separate refresh is needed. Ship later schema changes with:

```sh
gregale data-api deploy shop-data --migrations migrations
```

The starter's `migrations` directory (`migrate.mjs`, `release.mjs`,
`rpc-permissions.mjs` and `sql/`) works as is. Use `--release-command` for
another entrypoint. Release scripts run in the runtime image, so they may import
only Node built-ins and the bundled `pg` driver. Hidden files, `node_modules`
and links are not uploaded. A failed release blocks the deployment but keeps
migrations that already committed.

To convert an existing two-app project, rerun
`gregale data-api create NAME --database DB --resume --migrations migrations`
with your original issuer settings, and point the `migrate.command` in
`data-api.json` at `gregale data-api deploy`. Old instances keep serving the
previous schema until the new deployment is ready, so follow the
[compatible rollout](#accept-older-clients-during-a-compatible-rollout) guidance
for breaking changes. Coordinated refresh remains a two-app feature.

## Python and Go clients

Generate a typed client for backends written in Python or Go:

```sh
gregale data-api types shop-data --language python --output shop_data.py
gregale data-api types shop-data --language go --package shopdata --output shopdata/client.go
```

Both languages render from the same fingerprinted contract as the TypeScript
types. The deployed app exports the contract; no credentials reach your machine.
To render offline from a saved contract (for example in CI), pass
`--snapshot-file contract.json` instead of the app name. Commit the file and
use `--check` to fail CI when it is stale.

Generated files use only the standard library: Python 3.9+ with `urllib`, Go
1.21+ with `net/http`. Each file includes row, insert and update types, enums,
composites, typed RPC methods and a small client:

```python
import os

from shop_data import DataClient

backend = DataClient("https://shop-data.example.com", server_key=os.environ["SHOP_SERVER_KEY"])
open_orders = backend.orders().select("id,total").eq("status", "open").order("id", descending=True).limit(50).get()
```

```go
client := &shopdata.Client{BaseURL: "https://shop-data.example.com", ServerKey: os.Getenv("SHOP_SERVER_KEY")}
orders, err := client.Orders().Select("id,total").Eq("status", "open").Limit(50).Get(ctx)
```

Use `access_token` (Python) or `AccessToken` (Go) to call as an application
user, or `subject` / `Subject` with a server key to act for one. Updates and
deletes refuse to run without a filter. The clients never retry writes:
reconcile an uncertain write before you replay it, as with the TypeScript SDK.

A client without credentials refuses to send requests unless you opt in to
anonymous access, so a lost session never silently becomes public data. With
`anonymous=True` (Python) or `Anonymous: true` (Go), requests go out without
a token whenever the token source returns none (`None` or `""`). Call public
RPCs with GET:

```python
visitor = DataClient("https://shop-data.example.com", anonymous=True)
results = visitor.rpc_search_products({"q": "lamp"}, get=True)
```

```go
visitor := &shopdata.Client{BaseURL: "https://shop-data.example.com", Anonymous: true}
results, err := visitor.RPCSearchProducts(ctx, shopdata.SearchProductsArgs{Q: "lamp"}, shopdata.WithGET())
```

GET sends arguments in the query string: scalars as text, arrays as
PostgreSQL array literals and objects as JSON. `None`/`nil` arguments are
refused; omit them to use the function's defaults.

Both clients page with keysets, like the TypeScript SDK's `cursorPage`. The
last ordering column must be unique; `page`/`Page` replaces any ordering and
keeps your filters. With `before`, rows come back nearest-first in reverse
order:

```python
order = [{"column": "created_at", "ascending": False}, {"column": "id", "unique": True}]
first = backend.orders().select("id,created_at,total").page(order, 50).get()
following = backend.orders().select("id,created_at,total").page(order, 50, after=cursor_from(first[-1], order)).get()
```

```go
order := []shopdata.CursorOrder{{Column: "created_at", Descending: true}, {Column: "id", Unique: true}}
cursor, err := shopdata.CursorFrom(first[len(first)-1], order)
next, err := client.Orders().Select("id,created_at,total").Page(order, 50, cursor, nil)
rows, err := next.Get(ctx)
```

Go update structs leave `nil` fields unchanged. To set nullable columns to
`NULL`, list them in `SetNull`: `OrdersUpdate{SetNull: []string{"shipped_at"}}`.
The client refuses columns that are not nullable and updatable. Python
updates send `None` as `NULL`. To read relations embedded through `Select`,
decode into your own structs with `GetInto(ctx, &rows)`; Python rows are
plain dictionaries.

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
1 MiB request bodies, 16 MiB normal REST/RPC response bodies and a 15-second
upstream timeout. Type generation is
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
with `request_deadline_exceeded` for its total admitted deadline (or
`query_timeout` for an earlier upstream inactivity timeout), HTTP 503 with
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
The existing request-body, response-byte and response-row caps apply to bulk writes; the number
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
// If the network outcome is uncertain, first look up the receipt below.
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

### Read-only receipt recovery

Apply the append-only `0011_note_receipt_lookup.sql` migration and the normal
owner RPC permission workflow, then refresh the API and regenerate types. The
approved `read_note_create_receipt(uuid,text,text[])` function derives the subject
from the JWT and compares the original key, body and ordered tags. It returns
the stored original note snapshot without creating a note or reserving a key.
The function is `STABLE SECURITY INVOKER`, works in a SQL read-only transaction,
and restores its private SELECT-only RLS scope after lookup.

```ts
import { dataRequest, reconcileMutation } from '@gregale/data'

// pending was saved before the original write, under this API and identity.
const response = await dataRequest(
  client.readCreateReceipt(pending.key, pending.body, pending.tags)
    .select('id,body,subject'),
  { timeoutMs: 5000 },
)
const recovery = reconcileMutation(response, row =>
  row.subject === pending.subject && row.body === pending.body,
)
```

The helper classifies the completed lookup without executing another request.
It preserves selected types and returns `confirmed` with the original `row`,
`payload_conflict`, or `unresolved`. Its synchronous predicate binds a receipt to
the pending operation; keep the same API, subject, tenant where applicable, key
and complete payload. The lookup checks arguments that are absent from the
selected response. A visible payload mismatch uses HTTP 409, `PT409` and
`idempotency_payload_conflict`; other request failures retain their original
error/status and remain unresolved. Invalid or multiple rows also remain
unresolved, without exposing rejected rows or predicate exceptions.

An empty result cannot distinguish an uncommitted write, a hidden receipt or
owner purging. It never proves rollback. Preserve the original pending operation
and keep replay explicit under the retained-receipt contract. A lookup after
purging cannot recreate a note. Confirmation describes the original response,
including after a later edit or deletion of the business row. The
[order example](../examples/data-api-idempotency/README.md) provides the same
workflow with subject/tenant-scoped `read_order_receipt` and a typed client.
See [ADR-1669](adr/1669-data-api-read-only-mutation-recovery.md).

### Versioned replay expiry

The starter's append-only `0012_note_replay_expiry.sql` adds an explicit v2
contract. Apply it with the owner migration binding, run the existing RPC
permission workflow, refresh the serving schema and regenerate types. Install
the paired SDK bundle to use `createReplayKeyV2()` and the typed expiry results.
Existing v1 functions and cleanup behavior remain unchanged.

```ts
import { createReplayKeyV2, classifyDataError, dataRequest } from '@gregale/data'

// Persist once with the original API, subject and complete payload before send.
const operation = {
  contract: 'v2', subject: session.subject,
  key: createReplayKeyV2(), body: 'hello', tags: ['work'],
}
const result = await dataRequest(
  client.createWithTagsOnceV2(operation.key, operation.body, operation.tags),
  { timeoutMs: 5000 },
)
if (classifyDataError(result.error, { status: result.status })?.category === 'replay_expired') {
  // Retain operation. Read its receipt or resolve through business review.
}
```

`create_note_with_tags_once_v2(uuid,text,text[])` requires an RFC UUIDv7 key.
Its fixed deadline is the key's embedded millisecond timestamp plus **24 hours**.
Timestamps over **five minutes** ahead of the database clock are refused. A slow
client clock shortens its window; a clock within the future allowance can produce
up to 24 hours plus five minutes measured from acceptance. PostgreSQL checks
`clock_timestamp()` before and after the operation lock, after business work and
before returning. If work crosses the deadline, its database changes roll back.
Expiry is independent of receipt deletion and receipt creation time.

Expired writes return HTTP 410 / `PT410` / `idempotency_key_expired`. The SDK
classifies that exact contract as `replay_expired` with reconciliation guidance.
Do not replace the key or switch RPC versions after an uncertain write. Keys are
scoped by this contract and authenticated subject; changing the API, identity,
version or timestamp creates another operation namespace.

Use `client.readCreateReceiptV2(key,body,tags)` (SQL RPC
`read_note_create_receipt_v2`) for read-only recovery. A retained receipt returns
its original snapshot even after expiry. A visible payload mismatch returns the
existing `idempotency_payload_conflict`. A missing unexpired receipt returns zero
rows; a missing expired receipt returns the expiry error. `reconcileMutation`
keeps that outcome `unresolved`, with reason `replay_expired`. None of these
unresolved outcomes proves rollback or permits a new key. The browser starter
explicitly selects v2; `createNoteForm` preserves its v1 default for existing
callers and accepts `{ replayContract: 'v2' }` for opt-in adoption.

For owner maintenance, use the existing cleanup command with an explicit contract:

```sh
# Set MIGRATION_DATABASE_URL through the owner migration binding.
# Choose a past UTC cutoff. Preview, then use the same options with --apply.
npm run receipts:cleanup -- --contract v2 --before 2026-10-17T00:00:00.000Z --batch-size 100 --max-batches 1
```

V2 cleanup considers only generated key deadlines at or before both the past
cutoff and server time. It keeps bounded batches, skips locked rows and operations,
and removes snapshots without deleting notes. RLS permits no application receipt
updates/deletes; a maintenance trigger also refuses updates and premature owner
deletes. Owners can change their schema, so these guards do not cover arbitrary
owner SQL or truncation. Without `--contract v2`, cleanup retains its legacy v1
behavior described below.

Keep the fixed v2 replay window unchanged: extending it could revive purged keys.
A new policy requires a new contract version. Use a trustworthy database clock;
large backward clock jumps and restores losing committed receipts invalidate the
assumptions. This contract covers database effects under normal retained history,
with separate policies needed for external effects and disaster recovery.
Invalid UUID versions/variants return `PT400` / `idempotency_key_invalid`; excessive
future skew returns `PT400` / `idempotency_key_in_future`. See
[ADR-1670](adr/1670-data-api-versioned-replay-expiry.md).

### Versioned update and delete recovery

Apply `0013_note_mutation_receipts.sql`, run the normal owner RPC permission
workflow, refresh the serving schema and regenerate types. Install the paired SDK.
The new RPCs are opt-in; existing REST updates/deletes keep their prior behavior.

| Operation | Write RPC | Read-only receipt RPC | Starter client methods |
| --- | --- | --- | --- |
| Update | `update_note_once_v2` | `read_note_update_receipt_v2` | `updateOnceV2`, `readUpdateReceiptV2` |
| Delete | `delete_note_once_v2` | `read_note_delete_receipt_v2` | `removeOnceV2`, `readDeleteReceiptV2` |

Each call takes the original UUIDv7 key, note ID and expected version; update
also takes a nonempty patch containing only `body` and/or `priority`. These fields
must be a non-null string and a PostgreSQL integer respectively. The API derives
subject from the token. Keys share one update/delete namespace, so a successful
key cannot switch action, target, expected version or patch. Creation has its own
namespace. Keep the original API, identity, contract, action and all arguments.

```ts
import { createReplayKeyV2, dataRequest, classifyVersionedMutation, reconcileMutation } from '@gregale/data'

// Save this operation before its first request. Keep it unchanged for recovery.
const pending = {
  contract: 'note_mutation_v2', action: 'update', subject: session.subject,
  key: createReplayKeyV2(), id: note.id, expectedVersion: note.version,
  patch: { body: 'edited' },
}
const matches = (row: { id: number; subject: string; version: number; body: string }) =>
  row.id === pending.id && row.subject === pending.subject &&
  row.version === pending.expectedVersion + 1 && row.body === pending.patch.body
const response = await dataRequest(
  client.updateOnceV2(pending.key, pending.id, pending.expectedVersion, pending.patch)
    .select('id,subject,version,body'),
  { timeoutMs: 5000 },
)
const outcome = classifyVersionedMutation(response, matches)

// If uncertain, read its original receipt explicitly; this performs no write.
const receipt = await dataRequest(
  client.readUpdateReceiptV2(pending.key, pending.id, pending.expectedVersion, pending.patch)
    .select('id,subject,version,body'),
  { timeoutMs: 5000 },
)
const recovery = reconcileMutation(receipt, matches)
```

A successful update stores its post-update snapshot and increments the version
once. A successful delete stores the deleted snapshot, preserving its version.
Use `removeOnceV2(key,id,expectedVersion)` and `readDeleteReceiptV2` for deletion;
match the original ID, subject and **expected version**, without adding one.
Receipt confirmation describes the original operation, not the current note.

`classifyVersionedMutation` returns `applied`, `version_conflict`,
`payload_conflict`, or `unresolved` (including reason `replay_expired`). It executes
no request or retry. Stale, absent and hidden rows share HTTP 409 / `PT409` /
`idempotency_version_conflict`. Changing a successful key's request gives the
existing `idempotency_payload_conflict`. Failed mutations roll back and store no
receipt; a latest conflict alone cannot confirm an earlier uncertain write.
Invalid mutation arguments return `PT400` / `idempotency_mutation_invalid`.

V2 expiry remains 24 hours from the UUID timestamp, with five minutes of future
clock tolerance. Lookups can confirm retained expired receipts; writers reject
expired keys before and after lock waits, including after receipt deletion.
Preserve unresolved operations and never automatically change their key or row
version. To purge only expired update/delete receipts using the owner binding:

```sh
npm run receipts:cleanup -- --contract v2 --receipt-kind mutation --before 2026-10-17T00:00:00.000Z --batch-size 100 --max-batches 1
# Review the preview, then repeat with --apply to commit bounded batches.
```

Cleanup skips active operation locks and row locks and does not delete business
rows. Default creation cleanup remains unchanged. The clock, database-history
and owner-authority assumptions from the v2 expiry section also apply here.
See [ADR-1671](adr/1671-data-api-versioned-update-delete-receipts.md).

### Atomic note batch recovery

Apply `0014_note_atomic_batches.sql` after the earlier starter migrations. Run
owner RPC permission setup, refresh the serving runtime, regenerate types and
install the paired SDK. The opt-in `apply_note_batch_once_v2(key,operations)` RPC
commits 1–100 updates/deletes on **distinct notes** in one transaction. Every item
must supply `operation`, `note_id`, `expected_version`; updates also need a
nonempty `patch` with only `body` (non-null string) and/or `priority` (PostgreSQL
integer). Deletes must omit `patch`. IDs and versions are positive PostgreSQL
integers. Unknown fields, repeated IDs or invalid items reject the entire batch.

```ts
import { dataRequest, classifyVersionedMutation, reconcileMutation } from '@gregale/data'
import { prepareNoteBatch, noteBatchMatches } from './note-batch.js'

// Capture before sending; retain the original API/client, identity and contract.
const pending = prepareNoteBatch(session.subject, [
  { operation: 'update', note_id: first.id, expected_version: first.version,
    patch: { body: 'edited', priority: 2 } },
  { operation: 'delete', note_id: second.id, expected_version: second.version },
])
const matches = noteBatchMatches(pending)
const response = await dataRequest(
  client.applyBatchOnceV2(pending.key, pending.operations), { timeoutMs: 5000 },
)
const outcome = classifyVersionedMutation(response, matches)
if (outcome.outcome === 'applied') {
  // All original operations matched. results have validated typed snapshots.
  console.log(outcome.row.results.map(item => [item.index, item.operation, item.note.id]))
}

// After an uncertain response, explicitly read; this does not replay a write.
const lookup = await dataRequest(
  client.readBatchReceiptV2(pending.key, pending.operations), { timeoutMs: 5000 },
)
const recovery = reconcileMutation(lookup, matches)
```

`prepareNoteBatch` clones/freezes the input and creates one UUIDv7 key after
validation; it does not save pending work. An optional third argument restores
an original captured key without changing its deadline. Persist the whole
pending operation with its API/identity/contract context if recovery must survive
a reload. Use the same identity and API for lookup; token renewal may preserve
that identity. Never regenerate keys, update versions or split an uncertain batch
automatically.

One response row contains `request_key`, `subject` and ordered `results`. Each
result contains its zero-based `index`, `operation` and original note snapshot.
Updates advance the version once; deletes preserve the deleted version. The
matcher checks **every** result, including identity, ID, action, order, version
and update patch. A partial/malformed envelope cannot confirm the batch. The
SDK type-guard overload narrows generated JSON only after this runtime check.

All expected versions are checked under row locks before work begins. A stale,
absent or hidden note produces the same `PT409` /
`idempotency_version_conflict`. Any mutation, RLS, cascade or receipt failure
rolls back the entire batch. The saved results are capped at 1 MiB (1,048,576
bytes of JSON text); exceeding it gives HTTP 413 / `PT413` /
`idempotency_batch_result_too_large` and rolls back everything. Invalid arrays
return `PT400` / `idempotency_batch_invalid`. Existing API request/deadline limits
also apply. PostgreSQL sequences and external effects from custom code are
outside database rollback.

The ordered array is part of the receipt's identity: reordering or changing any
successful request returns `idempotency_payload_conflict`. Matching replay returns
the original envelope after later edits/deletions. Batch keys have their own
namespace, separate from creation and individual mutations. Locks are acquired
by ascending note ID; results follow submitted order. Customer triggers and
other transactions can still encounter ordinary lock errors.

The same v2 clock policy applies: 24 hours from the UUID timestamp and at most
five minutes ahead of database time. Writers reject expired keys even after
purge. `read_note_batch_receipt_v2` may confirm a retained expired receipt; an
absent live receipt returns zero rows and an absent expired one returns
`idempotency_key_expired`. Empty, conflicting and expired results do not resolve
an earlier uncertain request. Retain its original pending context.

Preview owner cleanup with the batch selector, then add `--apply` to commit:

```sh
npm run receipts:cleanup -- --contract v2 --receipt-kind batch --before 2026-10-17T00:00:00.000Z --batch-size 100 --max-batches 1
```

Cleanup skips active key/row locks and only purges expired batch receipts. It
never deletes business rows or reopens old keys. Budget storage for the full
ordered request and original snapshots. Existing SDK `bulkMutations` continues
to issue separate requests; use this explicit RPC when one database transaction
is required. See [ADR-1672](adr/1672-data-api-atomic-note-batches.md).

### Receipt retention and owner cleanup

For the legacy v1 contract, apply `0010_receipt_retention.sql` to add the cleanup
scan index. Choose a
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
Data API timeout configuration (the exact legacy statement-only policy or the
current statement/lock policy), connection/schema usage, table and
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

### Ranked note search

The notes starter's `0016_note_search.sql` adds an indexed, ranked search RPC.
Apply the migration, run the normal owner RPC permission workflow, refresh the
runtime and regenerate client types. Existing notes are indexed by the migration;
ordinary inserts, edits and deletes maintain the index in their transaction.

```ts
import { parseNoteSearchPage } from './note-search.js'

const response = await notes.search('running errands', { size: 20 })
if (response.error) throw response.error
const page = parseNoteSearchPage(response.data[0])
// Results contain current id, body, priority, created_at, version and rank.
if (page.has_more) {
  const next = await notes.search('running errands', { after: page.next_cursor!, size: 20 })
  if (next.error) throw next.error
  const following = parseNoteSearchPage(next.data[0], page.next_cursor!)
}
```

Search uses PostgreSQL's English dictionary, stemming and stop-word removal.
Every normalized query word must match. Input is plain text: Boolean operators,
phrase, prefix and fuzzy syntax are unsupported. Empty and stop-word-only queries
are invalid. Results sort by relevance (an integer score in millionths) and then
descending id. The RPC returns current note fields, without snippets or totals.
Requests support `.abortSignal(signal)` and preserve normal Data API errors.

Queries allow at most 1,024 UTF-8 bytes and 16 distinct normalized terms. Pages
allow 1–100 results (default 20), and cursors at most 128 ASCII bytes. Invalid
arguments return HTTP 400 with `note_search_query_invalid`,
`note_search_page_size_invalid` or `note_search_cursor_invalid`. Cursors bind
to the exact query text and verified subject. Keep API, identity and query
consistent across pages; restart when any changes. Pagination uses live data:
an edit that changes relevance can move a row between pages.

The internal term index uses subject/lexeme equality lookups that remain
indexable under RLS. Every candidate is joined to current notes under their
current RLS policy and checked again for a full-text match. Direct index reads
are hidden and direct writes are rejected. There are no definer functions or
required extensions. Common queries can still rank many matches; normal
statement and response budgets apply. The index consumes storage and adds work
to writes. Owner TRUNCATE, disabled triggers or dictionary changes require a
reviewed index rebuild. See [ADR-1677](adr/1677-data-api-ranked-note-search.md).

### Incremental note changes

The notes starter's `0015_note_change_feed.sql` adds a bounded change feed over
the existing Data API. Apply the numbered migration, use the normal owner RPC
permission workflow, refresh the runtime and regenerate the paired client types.
This captures future changes to `api.notes`; existing rows come from the initial
scan. Comments, tags and other related tables have separate lifecycles.

```ts
import { notesClient } from './notes.js'
import { parseNoteSyncCheckpoint, parseNoteChangePage,
  noteChangeCursorExpired } from './note-changes.js'

const notes = notesClient({ url, subject, accessToken })
const started = await notes.startSync()
if (started.error) throw started.error
let cursor = parseNoteSyncCheckpoint(started.data[0])

// First build a fresh staging cache with notes.syncRows({ afterID, size }).
// Scan in ascending id order until an empty page, then catch up from cursor.
const result = await notes.readChanges(cursor, 100)
if (noteChangeCursorExpired(result)) {
  // Restart the staging scan with a fresh startSync() checkpoint.
} else if (result.error) {
  throw result.error
} else {
  const page = parseNoteChangePage(result.data[0], cursor)
  // Re-fetch every distinct page.changes[].note_id through notes.db:
  // upsert current rows and remove ids absent under current RLS.
  // Apply the whole page before saving page.next_cursor locally.
  cursor = page.next_cursor
  // Explicitly request subsequent pages while page.has_more is true.
}
```

Events contain `sequence` (a decimal string), `operation` (`insert`, `update`,
`delete`), `note_id` and `version`. They identify rows to refresh and never
contain historical note bodies. Re-fetch ids even for deletion events: a newer
row state may already exist by the time the page is applied. Fetches use normal
RLS; an absent row is removed from the cache. A page can split a multi-row SQL
transaction. This provides eventual synchronization rather than a frozen client
snapshot. Persist the cursor only after applying all page invalidations.

Per-subject transactional counters ensure a later commit cannot skip an earlier
pending write. Rollback removes its changes and counter advancement together.
Writes by the same subject serialize at the counter and can encounter ordinary
SQL lock/deadlock errors. Other subjects have independent streams. Polling takes
no stream write lock and creates no counter row for an empty subject.

Each subject retains its latest 10,000 events. Older positions return HTTP 410,
code `PT410`, message `note_change_cursor_expired`. Restart the initial scan on
this exact result; failed requests do not advance the cursor. High write volume
can expire history during bootstrap. Stream watermarks remain after deletion of
all notes, and stream storage grows with the number of subjects with history.

Pages contain at most 100 events. Opaque cursors are at most 128 bytes and bind
to the feed instance and verified subject. Wrong-context, malformed and future
cursors return HTTP 400 / `note_change_cursor_invalid`; invalid sizes return
`note_change_page_size_invalid`. Missing/inconsistent history fails with HTTP
503 / `note_change_feed_unavailable`. Cursors are not authorization tokens.
Bind local caches to API and identity, and reset staging on identity changes.

Both RPCs work in read-only transactions. Private feed tables cannot be read or
modified directly through the API. Ordinary inserts, edits, deletes and approved
business RPCs capture metadata atomically, including owner changes of a note's
identity. Owner resets, TRUNCATE, history-losing restores and changed policies
require a reviewed reset/epoch strategy. See
[ADR-1676](adr/1676-data-api-incremental-note-change-feed.md).

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
    "setup_check": { "directory": "data-api-runtime", "command": ["node", "refresh.mjs", "check", "YOUR_BINDING_LOGIN_ROLE"] },
    "pause": { "directory": "data-api-runtime", "command": ["node", "refresh.mjs", "pause", "YOUR_BINDING_LOGIN_ROLE"] },
    "resume": { "directory": "data-api-runtime", "command": ["node", "refresh.mjs", "resume", "YOUR_BINDING_LOGIN_ROLE"] },
    "authorization": { "directory": "web", "command": ["npm", "run", "test:data-api-authorization"] }
  }
}
```

Set an unpredictable private `GREGALE_DATA_API_REFRESH_TOKEN` (64 lowercase
hexadecimal characters, generated from 32 random bytes), use a fresh token for
each new refresh, keep that token for recovery,
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
role and rejects drift. The database records the verified fingerprint, so runtimes whose serving fingerprint differs receive 409 before executing
application SQL. This fences structural contract drift; it does not identify
every old runtime version when the structural fingerprint is unchanged.

Pause waits for transactions admitted by the PostgREST pre-request hook,
including read-only GET transactions. Coordinated mode requires read committed
transaction isolation. Repeatable-read and serializable requests receive 503
`data_api_refresh_isolation_unsupported` before application SQL, because their
snapshots can predate a waited-for pause. This includes RPCs that configure a
stronger isolation level. Ordinary requests receive 503 while
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

This mode reserves one connection of the [engine pool](#engine-pool-size) for
its control reader and leaves the rest to PostgREST, so it needs a pool of at
least two. Engine-only SIGUSR1 reloads are rejected; use coordinated
sync. Owner/superuser DDL and direct database connections remain outside this
barrier. Serialize DDL through the workflow; fingerprints do not cover policy
bodies or every behavioral change. Upgrade every runtime sharing the binding
before relying on the barrier. Real hosted multi-replica qualification remains
pending until staging is configured.

### Inspect setup and recovery

Add `coordination.setup_check` as shown above to run the shipped read-only owner
check before each coordinated sync, including recovery. It verifies installed
control state, owner SELECT/UPDATE privileges, the ability to `SET ROLE` to the
binding login, a restricted serving role, and access to both hook functions with protected
ownership, SECURITY DEFINER and their fixed `pg_catalog` search path.
The helper uses a read-only transaction and rolls it back; it never installs,
pauses, releases, or adopts a barrier. Run it directly when diagnosing setup:

```sh
node data-api-runtime/refresh.mjs check YOUR_BINDING_LOGIN_ROLE
gregale data-api sync notes-data --config data-api.json --check
# Check an existing owned pause without restarting or repeating migrations:
gregale data-api sync notes-data --config data-api.json --resume-only --check
```

`sync --check` requires coordination and a `setup_check` command. It validates
workflow configuration, the application's coordinated runtime and phase, and
owner setup. For a new refresh it also compares the live schema and saved
baseline. It does not pause, run migrations/permissions/client checks, restart,
write types, or release traffic. Private schema inspection uses the existing
owner-authenticated app task, which can wake an idle app. Commands in the JSON
are trusted project code: use the shipped read-only helper for `setup_check`.
Older configurations remain accepted; without this field they do not perform
the additional owner privilege check. All coordinated syncs now reject an
unsupported runtime or wrong phase before invoking the pause command.

Inspect the serving runtime without database credentials or owner commands:

```sh
gregale data-api status notes-data
gregale --json data-api status notes-data --baseline schema.candidate.json
```

Configure the normal application JWT in `GREGALE_DATA_API_ACCESS_TOKEN`. The
private refresh token in `GREGALE_DATA_API_REFRESH_TOKEN` is optional for status;
provide the original token to prove ownership of an existing pause. Alternate
variables use `--access-token-env` and `--refresh-token-env`. Values never appear
in status output; neither token belongs on the command line.

| State | Meaning and next action |
| --- | --- |
| `active` | Serving and registered fingerprints agree, and traffic is admitted. After a lost resume acknowledgement, compare with the previously verified candidate; do not repeat migrations merely to recover the acknowledgement. |
| `paused_owned` | The supplied token owns the pause. Correct checks and run sync with `--resume-only`, the same baseline and token. |
| `paused_unowned` | The token is absent or belongs to another refresh. Retrieve the original token; a lost token requires owner-controlled recovery. |
| `contract_mismatch` | The serving runtime differs from the registered contract. Inspect deployment/schema changes before another update. |
| `coordination_disabled` | The runtime has no coordinated admission protocol. Install the barrier and upgrade/enable the runtime before coordinated sync. |

Missing or malformed state and unreachable runtimes fail status rather than
reporting active traffic. The optional baseline comparison is separate from
admission state: an active runtime may legitimately differ from an older
baseline. Status queries one serving runtime and the durable barrier; it does
not attest every replica, live owner DDL, or application check completion. An
active matching candidate is evidence that release was observed, not proof
that this client received the original acknowledgement. Status is inspection;
`--resume-only` remains the action that repeats required verification.


### Replay authorization scenarios against a hosted API

Saved request collections can also verify the hosted Data API. Map the same
identity names to application JWT environment variables in a non-secret JSON
file, for example `data-api.identities.json`:

```json
{
  "alice": "ALICE_TEST_JWT",
  "bob": "BOB_TEST_JWT"
}
```

Configure those environment variables through your local environment or CI
secret store. Do not write JWTs, account keys, or refresh tokens into this file,
the collection, command arguments, or URLs. Use dedicated test users whose
subjects and tenant claims match the fixtures; local `data-api.dev.json`
identity subjects can match those hosted users so the same collection works in
both environments. Assertions and request bodies remain project-owned test
data. JWT payload decoding checks distinct subjects locally; the hosted runtime
verifies signatures, issuer, audience and token validity.

```sh
gregale data-api replay notes-data \
  --collection data-api.requests.json \
  --identities data-api.identities.json --scenario notes-rls
gregale --json data-api replay notes-data \
  --collection data-api.requests.json --identities data-api.identities.json
```

Omit `--scenario` to replay all named scenarios. Version 1 collections also work
as `default`. The selected requests must be nonempty and exercise at least two
distinct JWT subjects. An unknown scenario, invalid collection, missing JWT or
single-subject selection fails before app lookup or HTTP replay. Hosted replay
requires Node 22+, a logged-in Gregale CLI, and the app HTTPS origin; it needs
neither Docker nor database credentials and installs no Node dependencies.

Before scenario requests, replay requires unauthenticated and invalid-JWT
OpenAPI requests to return 401 and verifies each selected identity against the
authenticated serving-schema endpoint. Failure of those probes prevents all
scenario writes. These checks ensure that denial assertions cannot pass merely
because the identity tokens are rejected. Each scenario has its own capture
variables, using the same substitution and exact status/JSON assertions as local
replay. Results report assertion names, HTTP statuses, matching flags and capture
names, without response bodies, capture values, decoded claims or token values. JSON
output uses `replay_result`, `replay_scenario_summary` and `replay_summary` events;
the summary includes authentication probes. Any failed assertion, transport
failure or interruption returns a nonzero exit status. Requests are not retried.

**Replay executes saved writes and RPCs against the selected hosted app.** Use
isolated test data and scenarios that clean up their own rows and can safely be
repeated after failure. A failed replay does not roll back completed requests or
perform automatic cleanup. Later scenario steps still run after an assertion
failure, which lets explicit cleanup steps execute; steps depending on missing
captures fail before sending their requests. Use a staging app for destructive
fixtures. The replay deadline defaults to five minutes, configurable with
`--timeout` up to one hour. Each request has a 30-second deadline; scenario
responses are bounded to 1 MiB and serving-contract reads to 8 KiB. Only the
configured HTTPS origin and `/rest/v1/` request paths are allowed, with redirects
rejected. Standalone replay supports an optional private refresh token through
`GREGALE_DATA_API_REFRESH_TOKEN` or `--refresh-token-env` when verifying a pause.
It never pauses or releases the barrier itself.

Replace the custom `coordination.authorization` command with required replay:

```json
{
  "authorization_replay": {
    "collection": "data-api.requests.json",
    "identities": "data-api.identities.json",
    "scenarios": ["notes-rls"]
  }
}
```

Place this object inside `coordination`. Configure exactly one of
`authorization` and `authorization_replay`; existing command-based workflows
remain supported. File paths are relative to the sync configuration. Omit
`scenarios` to select the entire collection. Types output cannot replace either
replay input. `sync --check` validates collection/scenario selection and identity
subjects offline without sending scenario requests.

Coordinated sync validates replay inputs before pause, then runs the selected
scenarios after fresh refresh, schema compatibility and client checks. Native
replay always uses the coordinator's private token (including a custom
`coordination.token_env`) and pins every scenario request to the verified
candidate fingerprint. Identity probes must confirm an owned paused barrier and
that candidate's serving fingerprint. Conflicting per-request contract pins are
rejected, and schema-contract rejection cannot count as a successful expected
409 assertion. Assertion reports go to stderr so JSON sync stdout remains its
final receipt. Failure leaves the barrier paused; fix the scenarios or application
and rerun `sync --resume-only` with the same private token. Recovery replays all
required scenarios without repeating migrations.

Passing replay verifies the project's chosen assertions, not every policy or
replica. Local PostgreSQL and HTTPS fixture tests do not establish live hosted
multi-replica qualification; that remains pending a configured staging target.

### One CI check for compatibility and generated types

Use `data-api check` to compare a committed contract baseline and TypeScript
file with the current database in one read-only check:

```sh
gregale --json data-api check notes-data \
  --baseline schema.json --types client/src/database.types.ts
```

Install Node.js 22 or newer on the CI runner and authenticate the Gregale CLI
through the environment. The hosted check starts one owner-authenticated,
deployment-attached `types --snapshot` task using the app's restricted binding.
It renders TypeScript locally from that exact snapshot using the embedded shared
generator; there is no second database inspection or npm installation. A passing
check requires zero unapproved breaking changes and byte-for-byte current types.
It does not migrate, refresh, pause or resume the app, and never creates or
rewrites the baseline or types file.

The report includes both fingerprints, whether the structural contract drifted,
ordered changes with compatibility reasons, breaking/approved/unapproved counts,
and the types status (`current`, `stale`, `missing` or `too_large`). Hosted reports
also identify the inspection task and deployment. `--json` emits a version-1 JSON
report even when valid inputs produce failing checks. Reports omit generated
TypeScript and file contents. Invalid inputs, failed/truncated inspection tasks,
and generator failures emit the CLI's usual error diagnostic and exit nonzero.
The deadline defaults to two minutes for the entire command; `--timeout` accepts
up to one hour, with local rendering additionally bounded to 30 seconds.

Removed relations or columns, changed types, new required insert fields and the
other existing `data-api diff` breaking rules fail the check. Compatible changes
can pass after the types file has been regenerated. Editing types while leaving
the fingerprint header intact still fails the exact comparison. Missing and
oversized types files also fail without being written. `--approval FILE` uses
the same reviewed path/reason approvals bound to both fingerprints as `diff`;
it only approves the named breaking changes and never waives stale types.

For an offline check, export the candidate snapshot once and omit the app name:

```sh
gregale data-api types notes-data --snapshot --output candidate.json
gregale --json data-api check --current candidate.json \
  --baseline schema.json --types client/src/database.types.ts
```

The second command needs Node.js but no Gregale credentials, network, Docker,
or database access. It checks the supplied candidate; it does not establish
that the candidate is still the live hosted schema. Existing hosted runtimes
with snapshot export are supported without redeployment. Local CI can produce
its candidate from project migrations before running the offline check:

```sh
gregale data-api dev --port 0 --once --output .gregale/ci-database.types.ts
# dev writes disposable CI types and its current contract outside the baseline.
gregale --json data-api check --current .gregale/data-api-contract.json \
  --baseline schema.json --types client/src/database.types.ts
```

Commit a reviewed baseline outside `.gregale`, as described above. Keep an old
baseline when you need CI to preserve evidence of an approved breaking change;
replace it only through your normal review. Do not regenerate committed types
inside the check job: generate them during development and commit the result.
Structural checks do not cover RLS policy bodies, arbitrary RPC behavior, or
whether every serving replica has refreshed. Keep authorization replay and
coordinated refresh checks for those concerns.

### Check liveness, readiness and recent failures

```sh
gregale data-api health notes-data
gregale --json data-api health notes-data --since 15m --timeout 30s
```

The read-only health report combines public process and readiness probes with
the existing owner-authorized retained log sample. Account credentials are sent
only to the management API. Probes follow no redirects and require no application
JWT. The default recent window is 15 minutes; one 30-second deadline covers the
report (`--timeout` accepts up to one hour). Individual public probes have a
five-second deadline. Probing may wake a parked app through normal routing.

`/livez` returns `200 {"version":1,"live":true}` when the gateway can handle the
request. It performs no engine, JWT or database work. `/healthz` retains engine
SQL readiness and returns HTTP 200 or 503 with `ready`, `live`, `version: 1`,
safe `checks` and an `admission` snapshot. Both bypass request admission and use
`Cache-Control: no-store`. Liveness does not prove startup completion or database
availability; startup failures and engine exit still stop the production runtime.
Keep deployment health checks on `/healthz`.

The observations have deliberately separate meanings:

| Field | Values and meaning |
| --- | --- |
| `checks.engine` | `reachable` means a TCP connection to the loopback engine listener succeeded; `unreachable` means it failed or timed out. It does not test SQL. |
| `checks.sql` | `ready` means the pinned engine's `/ready` check succeeded; `unready` means it returned a failing status; `unknown` means no status arrived before the deadline. The runtime disables PostgREST's DB listener, so readiness checks SQL connectivity through its existing pool. |
| `checks.database` | `reachable` means SQL readiness or the separate coordinated refresh reader succeeded during this probe; `unknown` means connectivity could not be established. It never infers a database outage from SQL unreadiness. |
| `checks.refresh` | `disabled`, `active`, `paused` or `unavailable`. A deliberate pause remains visible even when SQL is ready. An unavailable refresh reader makes readiness fail. |
| `admission` | Current `active`, `queued`, `capacity` and `queue_capacity` for this runtime. Full active capacity is evidence of admission saturation, not proof of SQL pool exhaustion. |
| `pool` | An allowlisted observation from the engine's private loopback metrics endpoint, with its own `checked_at` timestamp. `state: "observed"` includes `capacity`, `available`, `waiting` and `timeouts_total`; `state: "unknown"` omits all counts and includes a fixed reason. |

The engine pool has capacity two, or one with coordinated refresh (the separate
refresh reader retains the other connection). `available` counts existing idle
connections in the engine's lazy pool. It is not unused configured capacity:
`capacity - available` cannot be used as an active-connection count. Zero
available connections alone does not change current health. Positive `waiting`
records pending pool acquisitions and adds the CLI finding `pool_waiting`.
Waiters can include the SQL readiness probe itself; they are distinct from
the gateway's HTTP admission queue and do not count only customer requests.

`timeouts_total` is a cumulative engine acquisition-timeout counter. It may reset
on engine restart and does not establish timeouts within the CLI's `--since`
window. A positive counter or a reset does not add a current or recent failure.
Acquisition timeouts include engine admin/health work, including diagnostic probes.
Pool observations do not measure SQL execution time, acquisition latency or
database-wide connection usage.

Metrics callers have a two-second deadline and cap responses at the existing
8 KiB health-response bound. The pinned engine checks SQL twice before exporting
metrics when its DB listener is disabled, so saturation can make this observation
unknown. Reads reuse the engine pool, share in-flight work even after caller
timeout, and have the existing 15-second hard HTTP cap (including continuous
chunks). Completed observations are not cached. SQL work can outlive HTTP
cancellation. The metrics endpoint stays private and its raw response is never
forwarded.
Fixed unknown reasons are `unavailable`, `timeout`, `unsupported`, `too_large`
and `invalid_metrics`. Missing, duplicate, fractional, negative, unsafe-integer
or inconsistent required metrics fail the observation rather than producing zeros.
Unrelated metric series are discarded. Required pool metrics must be unlabelled.

Pool telemetry does not affect `/healthz` SQL readiness or its HTTP status.
The CLI retains valid readiness when pool metadata is unknown or malformed,
marks coverage partial (`pool_unknown`, `pool_invalid` or `pool_unsupported`),
and exits 1. A versioned older runtime without `pool` is unsupported for this
observation; update that runtime. Valid counts appear in `readiness.pool` in JSON
and the text report. Each timestamp belongs to its own probe; concurrent fields
are observations rather than a single atomic snapshot.

Runtime observations run concurrently with a two-second caller deadline. Engine
SQL probes share one in-flight admin request per runtime, retained until it
finishes or the existing 15-second query deadline; a caller timing out cannot
immediately create another pool waiter. Refresh health reads likewise stay shared
until the reserved reader settles. No new database connection pool is added.
PostgREST 14.3's admin `/live` also checks SQL in this configuration, which is why
the independent engine observation uses TCP. Already-started engine SQL work can
outlive an HTTP probe deadline.

JSON reports separate `current_findings` from `recent_findings` and include the
validated diagnostics report with its sample coverage. Current findings include
admission saturation, engine pool waiters, refresh pauses, engine reachability and SQL readiness;
recent findings include observed overloads, query timeouts, refresh pauses,
unavailability, response-size limits and user rate limits. `failures.refresh_paused` and `failures.refresh_unavailable` refine
the existing aggregate `failures.unavailable` count. Recent failures do not mark
a recovered runtime unhealthy. No matching retained records are not proof that
no failures occurred. Logs that are unavailable, truncated or contain gaps remain
explicit partial coverage; valid current observations are retained.

The command exits 0 for a healthy current observation and a completely read log
sample, and 1 for degraded/unavailable/unknown current health or partial coverage.
An older runtime without the versioned health response is reported as unsupported
and partial; redeploy the updated runtime. HTTP errors and malformed/oversized
responses expose fixed findings only, never raw response bodies. Public probes
can reach different replicas; neither the report nor the retained sample proves
the health of every deployment or replica, and the fields are observations rather
than an atomic snapshot. `data-api status` continues to inspect coordinated
refresh ownership and fingerprints.

### Summarize request logs and look up request IDs

`data-api diagnostics` summarizes existing safe request records without changing
the API or adding an application endpoint:

```sh
gregale data-api diagnostics notes-data --since 15m
gregale --json data-api diagnostics notes-data --deployment DEPLOYMENT_ID \
  --since 2026-10-11T09:00:00Z
gregale data-api diagnostics notes-data \
  --request-id 12345678-1234-4234-8234-123456789abc
```

Hosted diagnostics use the existing owner-authenticated runtime log API with
`follow=0`. They never send an application JWT, inspect the database, restart
an app or alter refresh ownership. The normal deployment-filter plan rules
apply. `--since` accepts the existing positive duration/day forms or an RFC3339
timestamp. The CLI also applies the lower bound to the request record's own
`time`. Without it, diagnostics read the available retained sample. The hosted
read deadline defaults to two minutes; `--timeout` accepts up to one hour.

The version-1 JSON report includes request counts, HTTP errors, server errors,
aborted requests, error-rate percentages and nearest-rank p50/p95/p99 gateway
latencies. `summary` covers all recognized routes; `data_requests` covers REST
and RPC only, keeping health/schema/OpenAPI traffic out of that traffic total.
`by_route` reports each fixed route category separately. `authentication` covers
records with `outcome: "authentication_failed"`, including their latency.
`failures` counts authentication failures and gateway-reported query timeouts,
engine/serving-contract unavailability, contract mismatches and upstream errors.
It does not inspect PostgREST error bodies to infer SQL error causes.

Errors mean HTTP status 400 or greater, or an aborted response; an aborted error
is counted once even when an error status was already sent. Latencies include
aborted requests and measure the gateway's local response finish/close interval,
not SQL execution, client receipt, network latency or platform wake time. Empty
groups report null percentiles and error rate, rather than a healthy zero.
Percentiles are exact for the observed sample; small samples have coarse ranks.

Updated runtimes also attach `timings` to admitted requests. Each entered phase
has `duration_ms` and `complete`: `verification` covers JWT verification and
gateway authorization/contract checks; `refresh` covers the refresh barrier;
`readiness` covers authenticated schema readiness probes; `signing` covers the
engine JWT; `upstream` covers engine HTTP dispatch until response headers arrive;
`streaming` covers response headers through the gateway's finish/close event.
These are sequential gateway intervals. Engine wait can include request upload,
transport, pool wait and SQL work; it does not isolate SQL execution or pool wait.
Admission queue wait remains separate in `admission.wait_ms`.

Every statistics group exposes `timings.observed_requests`, `without_timings`
and `by_phase`. Each phase reports `observed`, `completed`, `interrupted`, and
completed-only `latency_ms` p50/p95/p99. A normally returned error is a completed
phase observation. Cancellation or timeout marks the active phase incomplete;
earlier completed phases still contribute samples. Interrupted elapsed times are
lower bounds, reported separately in `interrupted_elapsed_ms` in JSON. Phases
never entered are absent, not zero. Legacy logs and requests rejected before
admission or bypassing it (including health probes) count as `without_timings`.
Missing completed samples produce null percentiles (`n/a` in text).
Phase populations can differ, so their percentiles cannot be added to reconstruct
the request-latency percentile.

New runtimes also enable PostgREST 14.3 timing and log `database_timings` when
a request is dispatched to the engine. Version 1 uses source
`postgrest_server_timing`, state `observed`, and five `phases`: `jwt`, `parse`,
`plan`, `transaction` and `response`. Each has `duration_ms` and `complete: true`.
`transaction` combines pool acquisition, connection/session setup, hooks, SQL and
result transfer. `plan` measures engine SQL generation, not PostgreSQL planning.
Both `sql_execution` and `pool_wait` are explicitly `unknown`: the pinned engine
does not isolate them, and subtracting gateway time cannot recover them.

The header is private and strictly parsed; raw headers and descriptions are never
logged or forwarded. Errors can omit it. A dispatched request with no engine
headers records `state: "unknown", reason: "no_response"`; absent or invalid
timing headers use `missing_header` or `invalid_header` and contain no durations.
A downstream abort or response limit after engine headers preserves the completed
engine measurement while gateway streaming remains incomplete. Engine timing is
not independent evidence of SQL commit or client receipt. Measurements belong to
one request UUID and timestamp; there is no cross-request cache.

Statistics groups add `database_timings` with `version`, `source`,
`observed_requests`, `unknown_requests`, `unknown_by_reason`, `without_timings`,
`by_phase`, `sql_execution` and `pool_wait`. Phase statistics use completed-only
percentiles. Unknown measurements and legacy/bypassed requests never become zero
latencies. `data-api health` includes the REST/RPC sample under
`diagnostics.data_requests.database_timings` and shows combined transaction
percentiles in text. These are retained sample observations; missing timings do
not change current readiness or make an otherwise complete report partial.
Rebuild the runtime from the updated template to enable this on an existing app.
See [ADR-1731](adr/1731-data-api-database-timing-telemetry.md).

Request IDs are deduplicated across the sample. `--request-id UUID` narrows all
summaries and includes the matching safe metadata record in `matches`. A missing
match produces an empty sample and succeeds when the input was otherwise read
cleanly; absence from retained logs is not evidence that a request never ran.
Unknown log fields are discarded, and route/method/outcome/error-code values
must belong to the runtime's fixed vocabulary. Reports contain no raw log lines,
SQL, table/function names, subjects, JWTs, headers, bodies or query strings.

For offline analysis, save runtime JSON logs and omit the app name:

```sh
gregale --json logs notes-data --since 15m > request-logs.ndjson
gregale --json data-api diagnostics --file request-logs.ndjson
```

The file can contain raw runtime NDJSON request records, JSON log envelopes with
a `line` field, and unrelated application log lines. It must be a regular file;
it is never rewritten. Offline diagnostics require no Node.js, Docker, Gregale
credentials or network. Do not combine `--file` with an app name or deployment
filter. Protect saved logs as application data even though the diagnostic output
omits arbitrary log content.

Analysis is bounded to 16 MiB of input, 100,000 input records and 64 KiB per line
or SSE frame. `input_records`, `ignored`, `invalid`, `duplicates`, `filtered` and
`gaps` describe what was read. Runtime log gaps, malformed request records,
unsupported frames, exceeded bounds, missing hosted terminal frames and read
failures mark `partial: true`, with fixed `partial_reasons`, and exit nonzero
while preserving the safe report. Timeouts and interruptions also retain partial
results. Clean input succeeds even if the observed requests include HTTP errors;
this command is a diagnostic summary, not an application health gate.

Hosted coverage is always `retained_sample`; offline coverage is `provided_file`.
`partial: false` means the available input was processed cleanly, not that every
request or serving replica was captured. These summaries are not durable metrics
or a complete traffic audit. Use existing app metrics and log archives/drains
when you need longer retention. Live hosted qualification still requires a
configured staging target.

### Classify SDK errors

`@gregale/data` exports `classifyDataError(error, { status })` for stable SQL,
authentication, permission, contract, deadline, cancellation, response-size and availability
categories. Pass `result.error` and `result.status`; successful null errors return
`null`. Caught `throwOnError()` errors can be classified without a status. The
helper preserves the original error and performs no requests or retries. See the
[SDK category and recovery reference](../sdk/data/README.md#classify-errors-for-application-recovery).
Use curated messages for UI feedback. Treat recovery hints as application
choices: uncertain write outcomes require reconciliation before replay, and
contract failures require a reviewed client/server rollout. Neither empty RLS
results nor arbitrary 409 responses establish a permission or contract error.

### Validate untrusted RPC inputs

The SDK's opt-in `validatedRpc(input, parser, args => db.rpc(name, args))` helper
runs a synchronous application-owned argument parser before constructing a
request. The callback retains generated RPC argument and return types, including
selected projections. Invalid input returns a structured, redacted
`validation_failed` outcome without token lookup or transport. Valid input
returns `completed` with the original query result; database failures still need
the usual `result.error` check. Thrown request errors propagate unchanged.
See the [SDK runtime argument validation example](../sdk/data/README.md#validate-rpc-arguments-at-runtime).
Parser policies, UI field feedback, retry settings and write reconciliation stay
application-owned. Database constraints and RLS remain authoritative.

Local acceptance for this helper runs in `scripts/test-data-api.sh --integration`
with the other Data API suites. `tests/data-api/validated-rpc.test.mjs` generates
types from its disposable database and compiles the TypeScript client before
executing it through PostgreSQL, PostgREST and the JWT gateway. The suite verifies
argument rejection, projected/scalar results, defaults, two-subject RLS, SQL
privileges and constraints, error classification and explicit retry suppression.
An independent SQL invocation counter covers rolled-back failures; a lost-response
case demonstrates read-only reconciliation of a committed write.

The notes starter's browser form is a runnable validated RPC example. It supplies
inline field feedback, curated request-error messages and typed note results.
Uncertain writes retain the same key and payload for read-only receipt lookup;
write replay is a separate explicit action and automatic retries remain disabled. Follow the
[starter form guide](../cmd/gregale/templates/data-api-starter/README.md#validated-rpc-form)
for same-user session handling, receipt-retention limits and in-memory state.

### RPC contract changes in CI

`data-api check` compares exposed RPC argument names/types/optionality and result
shapes alongside exact generated declaration freshness. Adding an optional
argument or a default is structurally compatible but still requires refreshed
types when the fingerprint changes. Removing or renaming arguments, adding a
required argument, removing a default, or changing argument/result types requires
review. Removing an approval annotation or execution permission removes the RPC
from the exposed contract. Introducing an overload also removes the function:
the current RPC contract supports one unambiguous function per exposed name,
even when the new overload is unapproved.

A fingerprint-bound approval does not waive stale types. Fresh types alone do
not approve a breaking change or prove an old client remains compatible. The
`tests/data-api/rpc-contract-drift.test.mjs` integration suite verifies both checks
against actual restricted-role catalog exports and compiled TypeScript consumers.

Argument default **presence** determines optionality; default **values** are not
encoded in the structural contract. Changing a default value, including to NULL,
can change SQL behavior while both compatibility and type freshness pass.
Generated ordinary RPC arguments accept explicit null and scalar results remain
nullable; this does not establish that every function body accepts those values.
Use application-owned validation and authorization replay for behavior, defaults
and function bodies, in addition to structural CI checks.

### Validate RPC response values

`validateRpcResponse(result, parser)` checks the entire successful RPC `data`
value using a synchronous application-owned parser. Its `validated` outcome
retains response metadata and supplies typed parser output; `request_failed`
preserves the original server/transport failure without parsing. A
`validation_failed` outcome contains only a safe `RPC_RESPONSE_VALIDATION_FAILED`
error and the original HTTP status, without rejected data or parser exceptions.
Null/undefined successful values also go through the parser. See the
[SDK response validation example](../sdk/data/README.md#validate-entire-rpc-responses).

Response validation happens after execution. It performs no requests or retries
and does not establish that a write rolled back. Reconcile an uncertain write
before replaying it. PostgreSQL/PostgREST acceptance explicitly verifies a
committed write whose response parser fails, along with scalar, JSON and
RLS-filtered row parsing and unchanged SQL permission failures.

### Team invitation example

The `data-api-teams` starter includes self-service member/admin invitation links.
Owners create, list and revoke invitations; a signed-in recipient joins under
their verified JWT subject without manual registry provisioning. Links expire,
are single-use, keep bearer secrets in URL fragments and store only hashes in
PostgreSQL. Recovery reuses one immutable operation; historical acceptance does
not restore subsequently removed membership. See the
[team starter guide](../cmd/gregale/templates/data-api-teams/README.md#self-service-invitations)
for sharing, acceptance, privacy and audit behavior. This is customer application
SQL, not a platform identity service or email delivery feature.

### Team CSV import and export

The team starter provides local CSV mapping and validation, reviewed create-only
imports, duplicate skipping and persisted batch progress. Owners/admins import;
owners see team history and admins their own, with resume restricted to the
original actor. Members may export their readable orders using the status filter.
See the [CSV workflow guide](../cmd/gregale/templates/data-api-teams/README.md#csv-import-and-export)
for application bounds, recovery, retention and live-export semantics. Other
schemas require application-owned parsers and reviewed RPCs.

### Reviewed team order actions

Team orders provides version-checked submit and fulfil RPCs with immutable
receipts and tenant-scoped history. Ordinary creates are draft-only, submission
freezes totals and fulfilment is final. Imported historical initial states remain
supported. See the [workflow guide](../cmd/gregale/templates/data-api-teams/README.md#reviewed-order-workflows)
for recovery, permissions and migration behavior. These rules belong to the
application starter; other domains need their own reviewed transitions.

### Team order summaries

The teams starter's `0012_team_order_summary.sql` adds
`summarize_team_orders(team_id, status_filter, minimum_total, maximum_total)`.
Apply the migration, run owner RPC permission setup, refresh and regenerate
types. The function returns one row with `tenant_id`, `order_count`,
`total_cents` and `by_status`. Each status bucket contains `status`,
`order_count` and `total_cents`, including zeros for absent groups.

```ts
import { teamsClient } from './orders.js'
import { parseOrderSummary } from './order-summary.js'

const team = teamsClient({ url, accessToken }, 'shop-a')
const filters = { status: 'submitted' as const, minimum: 100, maximum: 50000 }
const controller = new AbortController()
const result = await team.summary(filters).abortSignal(controller.signal)
if (result.error) throw result.error
const summary = parseOrderSummary(result.data[0], team.tenant, filters)
const exactCents = BigInt(summary.total_cents)
```

Status and inclusive amount filters are optional; null means absent. Amount
bounds use the existing per-order PostgreSQL integer range, 0–2,147,483,647
cents. Summaries include all matching visible rows, beyond ordinary page caps.
Counts and sums are decimal strings, preserving exact values above JavaScript's
safe-integer range. The parser validates the selected team, filters, status
order and agreement between overall and bucket totals, and freezes the result.

The read-only invoker RPC checks current membership and the order table's RLS.
Owners, admins and members may read. JWT tenant/role hints do not grant access;
revoked or absent membership returns HTTP 403 / `team_membership_required`.
Authorized empty datasets return zeros. Empty/null team IDs and invalid ranges
return HTTP 400 /
`invalid_order_summary`; enum/integer wire errors retain native database errors.
The client keeps native errors and cancellation and disables automatic retries.

All totals use one statement snapshot. In-flight reads can finish with an
earlier view during a concurrent revocation. Existing 15-second statement and
request deadlines apply; fixed result size does not cap scanning work. Errors
return no partial or cached totals. Status enum changes require updating the
paired client contract. See
[ADR-1678](adr/1678-data-api-team-order-aggregation.md).

### Team order webhooks

Reviewed team order transitions atomically enqueue signed delivery snapshots.
A separate application worker uses tenant-scoped trusted claims, leased retries
and finite dead-letter budgets. Owners inspect deliveries and replay failed
generations; keys and destinations stay in the worker environment. See the
[webhook guide](../cmd/gregale/templates/data-api-teams/README.md#transactional-webhook-delivery)
for worker setup, raw-byte HMAC verification and receiver deduplication. This is
at-least-once application delivery, with customer-owned scheduling and retention.

### Tenant webhook operations

Owners can pause/resume new leases, inspect queue health and enqueue age, and
review a bounded failure set before atomic batch replay. Active leases can still
finish while paused. Operation history and original-actor receipts support
recovery after response loss. See the [delivery operations guide](../cmd/gregale/templates/data-api-teams/README.md#tenant-delivery-operations)
for revision checks, effective expired states and retention responsibilities.

The `data-api-teams` starter includes named webhook subscriptions: owners select event
filters and approved configuration references, while separate workers deliver from
independent queues. Pending deliveries retain their original version and reference.
See the generated team README for private worker configuration, rotation, and the
interaction between per-subscription and team-wide pause.

Owners of the team starter can queue signed sample webhook tests and inspect
per-delivery attempts, retry eligibility and subscription changes. Tests use the same
private worker and respect pause controls. Missing-binding diagnostics help operators
restore approved configuration; HTTP acceptance is evidence for the pinned version
at that time, not a live health guarantee. See the generated README for sample
receiver handling and immutable operation recovery.

The team starter now supports personal saved order views with reusable status/total
filters and sorting. Owners and admins can preview selected submitted orders and
atomically fulfil a reviewed batch, with version checks, webhook fan-out, immutable
history and recovery for lost responses. A stale row rejects the whole batch. See the
generated team README for personal-view privacy, pagination and recovery semantics.

The team starter supports shared views published by owners/admins. Members can apply
published status/range/sort presets, inspect publication history, or create private
personal copies. Reviewed revisions and immutable receipts protect edits and copy
recovery. Shared views reuse existing queries, exports and bulk-review controls;
applying them does not change order permissions. See the generated README for copy
privacy and publication semantics.

### Schema changes and consumer impact

`data-api diff` can identify saved views and RPCs affected by changed contract
paths. Keep a customer-owned dependency manifest alongside your migrations:

```json
{
  "version": 1,
  "consumers": [
    {"kind": "saved_view", "name": "Submitted orders", "dependencies": ["api.team_orders.status", "api.team_orders.total_cents"]},
    {"kind": "rpc", "name": "Fulfil orders", "dependencies": ["api.team_orders", "api.fulfil_orders_once"]}
  ]
}
```

```sh
gregale --json data-api diff --baseline before.json --current after.json \
  --dependencies data-api.dependencies.json --check
```

The JSON report includes `impacts` with each affected consumer and its matching
changes, including breaking/approval status. Human-readable output lists affected
consumers. Relation or schema removals affect declared descendants; a relation
reference covers all its fields, and an RPC reference covers argument changes.
Names match exactly at contract path boundaries (`api.orders` does not match
`api.orders_archive`). Duplicate dependency matches report a change once.

Coverage is **declared dependencies only**: maintain this manifest when changing
views or RPCs. Gregale does not parse SQL bodies, discover stored user preferences,
or prove that unlisted consumers are unaffected. Declare enum and composite type
paths separately when used. Dependencies do not change the existing CI gate:
`--check` still fails on every unapproved breaking contract change, including
changes without a declared consumer. Offline comparisons need no staging access.

Dependency paths must exist in either comparison snapshot. Unknown paths fail
validation, so a misspelled field cannot silently disappear from the impact report.

### Rehearse migrations before deploying

Run a migration rehearsal from your project directory:

```sh
gregale --json data-api migration preview --baseline schema.json \
  --dependencies data-api.dependencies.json --replay data-api.requests.json \
  > migration-preview.json
```

The baseline is a previously reviewed `data-api types --snapshot` export or a
saved `.gregale/data-api-contract.json` from local development. All file arguments
are relative to `--directory` (default `.`), unless absolute. Install the pinned
SDK and client dependencies before running this command. It requires Node.js
22+, npm, and a local Docker daemon, as does `data-api dev`.

Preview rebuilds the configured migration history in disposable PostgreSQL 16,
loads configured local seed data, applies the existing RPC/RLS permission checks,
and verifies the required authorization scenarios against PostgREST. It compares
the resulting API with the read-only baseline, checks committed generated types,
runs configured client checks, and optionally replays a saved request collection.
`--scenario NAME` restricts the optional replay; required authorization scenarios
always run. `--output FILE` overrides the configured committed types path.

JSON mode emits one version 1 report on stdout, after container cleanup. It
includes the actual migration ledger (versions, filenames, SHA-256 checksums),
baseline and candidate fingerprints, schema changes, declared consumer impacts,
request assertion results, and per-step `passed`, `failed`, `skipped`, or
`not_run` statuses. Diagnostics and dependency installation output go to stderr.
Without `--json`, preview prints a readable summary. Exit status 0 means the
requested checks and cleanup passed; failures return a nonzero status for CI.
Invalid inputs and prerequisite failures can exit before rehearsal starts and
therefore before a report is available.

Unapproved breaking changes, stale types, client failures, or replay mismatches
fail the report, but do not suppress subsequent independent checks. Migration,
permission, or authorization failures stop checks that need a working API;
remaining stages show `not_run`. Omitted optional replay shows `skipped`.
`--approval FILE` accepts only existing fingerprint-bound compatibility approvals.
`--dependencies FILE` uses declared coverage; omitted dependencies show
`not_provided`. Unknown dependency paths fail comparison.

Preview does not update the baseline, committed types, or local accepted contract,
and does not persist local JWTs or identity receipts. Configured client commands
still execute locally and may write build/test artifacts. Replayed requests target the disposable API database.

By default, this is a **clean rebuild** from local migration history and seed data. It does
not clone an existing database or prove that an upgrade will succeed on production
rows, lock contention, or data volume. Use representative local seed data for
application checks, and rehearse data-dependent upgrades against a separate
representative database before deployment, or use the fixture-based upgrade mode below. Preview does not deploy or modify a
hosted Gregale database.

### Rehearse an upgrade with existing data

Add `--from-version N` to build the selected older schema before applying the
pending migrations:

```sh
gregale --json data-api migration preview --baseline schema-v10.json \
  --from-version 10 --fixture testdata/pre-upgrade.sql \
  --data-check testdata/post-upgrade.json --replay data-api.requests.json \
  > upgrade-preview.json
```

The selected version must exist in the configured migration directory. Preview
applies migrations through that version, configures the API role, and requires
its inspected contract fingerprint to match the baseline. Compatibility approvals
cannot bypass this source check. It then loads the SQL fixture into the older
schema, verifies that the fixture did not change the exposed API contract, and
applies only the later migrations using the existing transactional migration
ledger. The fixture can therefore reveal failures such as a new constraint that
cannot accept existing rows. Fixtures referencing future columns fail before the
upgrade starts.

`--fixture FILE` overrides the configured seed for this rehearsal. If omitted,
the development config's seed must be nonempty and compatible with the older
schema. Fixtures run with the local database owner, as configured seeds do; use
representative, data-only SQL. In upgrade mode, seeds run **before** pending
migrations and are not rerun afterward. The normal clean-rebuild preview remains
available by omitting `--from-version`.

Declare data-preservation and backfill assertions in a JSON file:

```json
{
  "version": 1,
  "checks": [
    {
      "name": "Existing notes preserved",
      "sql": "SELECT count(*) = 2 AS passed FROM api.notes WHERE subject = 'fixture-user'"
    },
    {
      "name": "Existing note content preserved",
      "sql": "SELECT count(*) = 2 AND bool_and(body IN ('first note', 'second note')) AS passed FROM api.notes WHERE subject = 'fixture-user'"
    }
  ]
}
```

For those checks, the older-schema fixture could contain:

```sql
INSERT INTO api.notes(subject, body)
VALUES ('fixture-user', 'first note'), ('fixture-user', 'second note');
```

Each assertion must be a single SQL query returning exactly one row and one
boolean column named `passed`, with the value `true`. False, null, wrong-shaped
results, SQL errors, writes, and multiple statements fail the assertion. Checks
run in separate read-only transactions with statement and lock timeouts, before
API authorization verification can create or remove its own test rows. These
owner-level checks inspect stored data; required API verification and saved
request replay continue to check application authorization separately.

A manifest may contain one to 100 uniquely named checks and must be a regular
JSON file under 64 KiB. Query text, returned values, fixture rows, and raw database
errors are not included in the report. All assertions run even when an earlier
assertion fails, and a data-check failure still allows independent API and client
checks to run.

The version 1 report uses `mode: "upgrade"` and adds source and fixture stages,
`upgrade.from_version`, the verified source fingerprint, source migration
checksums, pending migration checksums, and `data_checks` results. On an upgrade
SQL failure, the migration ledger reflects the source migrations that remain
after rollback. Data coverage is `declared` when `--data-check` is provided, or
`not_provided` with a skipped data stage when it is omitted. Data assertions can
also be used with a clean-rebuild preview.

This rehearsal covers the supplied fixtures and assertions. It does not clone a
hosted database or reproduce production data volume, concurrent traffic, or lock
contention. Baselines, committed types, fixture files, and local accepted contracts
remain unchanged, and disposable services are removed on success and failure.

### Build representative fixtures from a local or test database

Use `data-api fixture build` to generate rehearsal data from an explicitly
allowlisted slice of a PostgreSQL database. Supply the connection through an
environment variable, as you would other database bindings:

```sh
gregale --json data-api fixture build --config fixture-policy.json \
  --database-url-env DATA_API_FIXTURE_DATABASE_URL \
  --api-role gregale_data_api --output fixtures/before-upgrade
```

The command needs Node.js 22+ and npm. It installs the pinned embedded runtime in
a temporary directory and does not require Docker. The selected environment
variable holds the local/test database URL; the URL is never placed in arguments,
options files, fixture metadata, or diagnostic output. Remote test connections
use verified TLS. `--api-role` switches only contract inspection to the serving
role, so the generated contract reflects its privileges. The source account must
be allowed to select that role and read the declared source rows. Without this
flag, the contract reflects the source account's own privileges.

Declare each table, every exported column, root filters, and masking rules:

```json
{
  "version": 1,
  "tables": [
    {
      "table": "api.notes",
      "columns": ["id", "subject", "body"],
      "where": {"id": 10},
      "limit": 1,
      "mask": {
        "subject": {"kind": "hash", "group": "subjects"},
        "body": {"kind": "replace", "value": "sample note"}
      }
    },
    {
      "table": "api.comments",
      "columns": ["id", "subject", "note_id", "body"],
      "limit": 0,
      "mask": {
        "subject": {"kind": "hash", "group": "subjects"},
        "body": {"kind": "replace", "value": "sample comment"}
      }
    }
  ]
}
```

Filters are parameterized equality comparisons on allowlisted columns. A null
filter means `IS NULL`. `limit` selects root rows in primary-key order; its default
is 20, and zero means related rows only. Once roots are selected, the exporter
follows incoming and outgoing foreign keys between declared tables until the
related set is complete. Root filters and limits do not restrict relationship
closure. For example, the policy above includes comments attached to the selected
note even though the comments table has no roots. Rows are deduplicated by their
primary keys. A non-null reference to an undeclared or invisible parent fails the
export; the tool never silently drops required parents or truncates closure.
Undeclared child tables are not traversed.

Only allowlisted columns are selected. Include each table's primary, unique, and
foreign-key columns, along with required columns that have no database default.
Keep a sensitive required column in the allowlist and explicitly mask it. Columns
without masks retain their selected values; there is no automatic sensitive-data
detection or masking. Available rules are:

- `replace`: use a declared scalar `value`, converted by PostgreSQL to the column
  type. Use strings for exact large integers and decimal values.
- `null`: emit null for nullable columns.
- `hash`: use a named `group` to generate a 32-character text pseudonym. Hashes
  use a fresh unrecorded random salt per export; equal values in the same group
  map consistently within that package.

Hash masks support PostgreSQL text and varchar columns with room for 32
characters. Primary, unique, and foreign-key columns support keeping values or
hash masking; replacements and null masks on keys are rejected. Both sides of a
foreign-key pair must use the same masking rule and hash group. The exporter
checks selected key collisions and masked relationship consistency before writing
files. Arbitrary database constraints and triggers still apply when restoring
fixtures; choose replacement values that satisfy them.

Export runs in a read-only, repeatable-read source transaction. It preserves
source rows and sequence state. Identity values are emitted with
`OVERRIDING SYSTEM VALUE`, and the generated SQL resets ordinary ascending
identity/serial sequences on the target so subsequent API writes avoid exported
IDs. The first version supports ordinary and partitioned `api` tables with an
acyclic active table dependency graph and primary keys. Generated columns cannot
be exported directly. Custom descending or nonstandard-increment sequences,
cyclic/self-referencing row sets, partial/expression unique indexes, views, and
arbitrary SQL filters are rejected.

A policy must be a regular JSON file under 64 KiB with one to 50 declared tables.
Roots and complete relationship closure share the existing 1,000-row Data API
bound. Selected source values and generated SQL share the 1 MiB fixture bound;
generated assertions must fit the rehearsal's 64 KiB/100-check limits. The source
API contract must fit the existing 1 MiB schema-comparison bound. Exceeding a
bound fails the export without a partial package. Narrow roots or split policies
rather than relying on truncation.

The output directory must be new. All files are staged privately and published
together. It contains private files:

- `fixture.sql`: typed SQL containing only selected, explicitly masked values.
- `data-checks.json`: basic assertions for preserved keys and references.
- `contract.json`: the source API snapshot and fingerprint.
- `fixture.json`: row counts, column allowlists, mask kinds/groups, and checksums.

The source URL, filters, replacement values, and hash salt are not recorded in
metadata. The SQL and assertion files contain the kept or masked fixture values;
review them before sharing. A failed build preserves existing output directories.

Use the package directly in an upgrade rehearsal:

```sh
gregale --json data-api migration preview --from-version 10 \
  --baseline fixtures/before-upgrade/contract.json \
  --fixture fixtures/before-upgrade/fixture.sql \
  --data-check fixtures/before-upgrade/data-checks.json \
  --replay data-api.requests.json
```

The selected source migration version must reproduce the exported contract.
Generated fixtures contain a source fingerprint and payload checksum in their
header. Preview rejects edited payloads and mismatched source fingerprints before
loading rows. Hand-authored SQL fixtures continue to work. Add your own data checks
for business invariants and expected backfills; generated checks cover declared
keys and references, not every value or production workload.

### Inspect local query plans

`data-api explain` plans saved GET relation requests against a disposable local
rebuild, using their configured identities and RLS. Supply representative seed
data or a SQL fixture; writes, RPC calls and unresolved capture variables are
skipped. A report passes only when at least one read is planned and none fail.

```sh
gregale --json data-api explain --collection data-api.requests.json \
  --fixture fixtures/before-upgrade/fixture.sql > plans-before.json
# Add a reviewed index migration, then compare the same workload and fixture.
gregale --json data-api explain --collection data-api.requests.json \
  --fixture fixtures/before-upgrade/fixture.sql \
  --baseline plans-before.json > plans-after.json
```

Use `--scenario NAME` to select one saved scenario and `--config FILE` for a
custom development project. The command refreshes statistics in its disposable
database and requests estimated PostgREST plans with execution analysis disabled.
It reports scan/sort findings and candidate BTREE indexes for simple root filters,
ordering and direct JWT equality policies. Review suggestions for selectivity,
existing indexes, RLS and write cost before applying a migration. Complex policies,
embedded filters and partial/expression indexes require manual assessment.

Reports omit filter values, claims and raw SQL expressions. They include estimated
costs, rows, catalog names, request names and cleanup status. Costs are planner
units, not measured latency. Comparison requires matching fixture/seed, identities
and saved requests; use deterministic fixtures because matching SQL alone does
not guarantee identical data. Local data distributions and planner settings may
differ from production. Planning can evaluate functions declared immutable by the
schema. This command leaves generated types, saved contracts and collections
unchanged and enables plan responses only in its disposable PostgREST instance.

### Enforce query performance budgets in CI

Use the same deterministic fixture and saved collection for the approved baseline
and candidate migrations. Checked runs require a baseline and an explicit budget:

```sh
gregale --json data-api explain --collection data-api.requests.json \
  --fixture fixtures/before-upgrade/fixture.sql --baseline plans-before.json \
  --check --budget budgets.json > plans-candidate.json
```

```json
{
  "version": 1,
  "queries": [
    {
      "scenario": "default",
      "name": "Selective read",
      "max_total_cost": 100,
      "max_cost_ratio": 1.2,
      "max_scanned_rows": 1000
    }
  ]
}
```

Version 1 collections use scenario `default`; version 2 uses the saved scenario
name. Each rule needs at least one limit. All limits must be finite nonnegative
numbers, and cost ratios must be greater than zero. Duplicate selectors and
unknown fields are rejected. Equality at a limit passes. There is no implicit
regression threshold; choose limits appropriate for your workload.

Every planned read must have a budget, and every budget must resolve to a planned
read with a matching baseline query. A skipped, missing or failed budgeted query
cannot pass. When selecting `--scenario`, supply budgets for that selected workload.
Exceeded limits return exit code 1 after cleanup, with structured `budget_check`
violations and the existing index suggestions in the JSON report. Invalid inputs
fail before starting disposable services. Budgets and baseline files are read only.

`max_total_cost` uses the top-level estimated plan cost; `max_cost_ratio` bounds
candidate cost divided by baseline cost. For a zero-cost baseline, only a zero-cost
candidate passes that ratio limit. `max_scanned_rows` uses approximate scan work:
sequential scans count full relation rows from local statistics, while index,
bitmap heap, TID and foreign scans count estimated returned rows. Scan nodes are
summed once without estimating loop multiplicity or rows rejected by index filters.
This metric is not a measured physical-row count. Plans and budgets remain local
estimates and do not guarantee production latency.

### Verify reusable data integrity checks

`data-api verify` rebuilds the configured local migrations in a disposable database,
loads the configured seed or a current-schema SQL fixture, and runs named assertions
before authorization verification creates its temporary test rows. It then runs the
project's required authorization checks through the local API. No hosted database
or credentials are used, and types, contracts and input manifests stay unchanged.

```sh
gregale --json data-api verify --checks data-checks.json \
  --fixture fixtures/current.sql > verification.json
```

Reuse the same version 1 assertion manifest with migration preview's `--data-check`:

```json
{
  "version": 1,
  "checks": [
    {
      "name": "Required note bodies",
      "sql": "SELECT NOT EXISTS (SELECT 1 FROM api.notes WHERE body IS NULL) AS passed"
    }
  ]
}
```

A manifest contains one to 100 uniquely named checks, each with one SQL statement
returning exactly one row and one column named `passed`, containing boolean `true`.
Use `NOT EXISTS` for orphaned relationships, null fields or invalid tenant mappings,
and boolean aggregate comparisons for expected counts and totals. False results,
wrong result shapes and SQL errors fail the command with exit code 1. Queries run
in separate read-only transactions with 15-second statement and 5-second lock
limits; failures roll back and remaining checks continue. Reports contain names,
pass/fail results, summary counts, authorization status and cleanup status, without
SQL or data values. Invalid manifests fail before containers start.

Assertions run as the disposable database owner to inspect all fixture rows; they
are data integrity checks, not RLS impersonation. Tenant isolation is checked by the
project's configured API authorization scenarios. Configure those scenarios to
cover your identities and access rules. Local fixtures must represent the cases you
want to detect. For older-schema fixtures and pending upgrades, use
`data-api migration preview --from-version N --fixture FILE --data-check FILE`,
which also checks source contracts and migration compatibility.

### Verify identity-aware access rules

Use `data-api verify --access-checks access-checks.json` with a saved version 1
request collection or version 2 named scenario collection. You may combine it
with `--checks data-checks.json`; at least one manifest is required. Requests use
the project's configured identities through the disposable local API, after its
required authorization verification. Successful HTTP assertions require explicit
expected JSON: status 200 alone does not prove that a row was visible or changed.
Use `json: []` for RLS-filtered reads or mutations, and a follow-up owner read to
prove a forbidden mutation left the data unchanged.

```json
{"version":1,"requests":[
  {"name":"Bob cannot read Alice","identity":"bob","method":"GET",
   "path":"/rest/v1/notes?subject=eq.alice&select=id",
   "expect":{"status":200,"json":[]}}
]}
```

Scenarios run ordered requests with captures and independent variable scopes.
Use setup requests, member/admin reads, a membership-revocation request and a
subsequent denied read to verify access transitions. Mutations affect only the
disposable fixture; scenarios share that database, so use distinct test records
or explicit cleanup. When an assertion fails, remaining requests in its scenario
are marked skipped and the next scenario continues. Any failure or skipped
assertion causes a failing exit code. Reports include named identity-aware results
and `access_summary`, without paths, claims, response bodies or captured values.
Generated types, contracts and manifests are preserved. Checks prove only the
access cases represented by your fixture and expectations.

### Generate a guided verification package

Use `data-api verify init` offline with a saved schema contract and development
config. Choose a new output directory; existing files are never replaced:

```sh
gregale data-api verify init --schema .gregale/data-api-contract.json \
  --config data-api.dev.json --output verification --plans plans-before.json
```

The package contains `README.md` and `coverage.json`, plus applicable manifests:
`data-checks.json` for nonnullable fields and known API foreign-key orphan checks,
`access-checks.json` for owner/nonowner read drafts, and optional `budgets.json`
from a successful explain report matching the schema fingerprint. Budgets use
exact baseline cost/scan estimates and a growth ratio of 1; review tolerances and
keep the original report for `explain --baseline`. Missing manifests mean no
applicable templates were generated, not that the area is verified.

Access drafts use the first two sorted configured identity names and an unresolved
`{{fixture_value}}` placeholder. Choose actual ownership columns, fixture values
and expected owner rows before running them. Add admin, forbidden mutation and
membership-revocation assertions for your access model. These drafts deliberately
fail until edited. Configured claims, subjects and explain filter values are not
copied into the package.

Coverage stays `needs_review`: initialization executes no checks and does not infer
permission semantics. Relations without access templates are marked `not_generated`.
Integrity assertions are limited to 100 and access drafts to 50 relations (100
requests); each manifest is bounded to 64 KiB. Review larger schemas for omitted
checks. Views, unknown/cross-schema relationship targets, CHECK expressions,
composite FK match variants and business invariants need custom assessment. Checks
based on existing constraints are a starting point; representative fixtures and
application-specific assertions provide stronger migration coverage.

### Enforce customer-required verification coverage

`data-api verify coverage` evaluates an explicit policy against saved reports,
offline. It does not run checks or infer what a named check proves:

```sh
gregale --json data-api verify coverage --schema schema.json \
  --policy coverage-policy.json --verification verified.json \
  --plans checked-plans.json --baseline-schema previous-schema.json --check
```

```json
{
  "version": 1,
  "default_required": ["integrity", "access_read"],
  "relations": [
    {
      "relation": "api.notes",
      "required": ["integrity", "access_read", "access_mutation", "access_admin", "access_revocation", "performance"],
      "checks": [
        {"kind": "integrity", "name": "Required bodies"},
        {"kind": "access_read", "scenario": "default", "name": "Owner reads fixture"},
        {"kind": "performance", "scenario": "default", "name": "Concrete read"}
      ]
    }
  ]
}
```

Supported kinds are `integrity`, `access_read`, `access_mutation`, `access_admin`,
`access_revocation` and `performance`. Declare mappings for each required kind;
the example still lacks mutation, admin and revocation mappings and therefore
fails. Integrity names reference SQL results, access names/scenarios reference
identity-aware results, and performance names/scenarios reference budget-checked
queries for that exact relation. Every mapping for a required kind must pass.
These categories express customer intent: review the mapped SQL, identities and
request expectations yourself. A passing assertion does not automatically prove
that its declared category or table scope is correct.

Evidence must match the current schema fingerprint and report completed cleanup.
Only successful overall verification reports with passing authorization count;
skipped access assertions do not count. Performance evidence needs a successful
budget check covering planned reads. Plain explain reports do not count as budget
coverage. Reports are bounded to 1 MiB; policies to 64 KiB and 1,000 mappings.
Duplicate mappings, unknown kinds, ambiguous evidence names and stale schema
fingerprints are rejected. Use reports from the current release candidate; this
command does not attest report age, fixture suitability or test-code freshness.

Every relation in the current schema is inventoried. Relations without an explicit
policy entry inherit the nonempty `default_required` list, so a newly added table
cannot disappear from coverage. `--baseline-schema` marks newly added relations.
Explicit per-relation `required: []` waives requirements for that relation. Removed
relations still referenced by the policy require policy review. The report lists
verified/missing kinds per relation; `--check` returns exit code 1 for missing
requirements. Without `--check`, missing coverage is informational. Omitted evidence
files leave corresponding requirements missing. Generated initializer `coverage.json`
is a review guide, not this policy or evidence of passing checks.

### Run one local release check

`data-api verify release` runs migration preview, integrity/access verification,
query-budget enforcement and coverage validation in order, then emits one report:

```sh
gregale --json data-api verify release --config data-api.release.json \
  > release-report.json
```

```json
{
  "version": 1,
  "baseline": "schema-before.json",
  "current_schema": "schema-candidate.json",
  "checks": "data-checks.json",
  "access_checks": "access-checks.json",
  "collection": "performance-requests.json",
  "plan_baseline": "plans-before.json",
  "budget": "budgets.json",
  "coverage_policy": "coverage-policy.json"
}
```

The project defaults to the release config's directory. Optional `directory` is
relative to that directory; all other file paths are relative to the project.
Use `development_config` for a custom development config. At least one of `checks`
or `access_checks` is required; the baseline/current contracts, query collection,
plan baseline, budgets and coverage policy are required. The candidate contract
must describe the schema being rehearsed. Generate and review it beforehand, and
commit matching client types and install client dependencies for migration preview.
The release check preserves those files and does not deploy or publish anything.

A clean rebuild uses the project's configured seed throughout. If an explicit
`fixture` is supplied, it must be that same configured development seed. An upgrade
uses `from_version` and `upgrade_fixture` for older-schema rehearsal, plus a required
current-schema `fixture` for standalone integrity/access and performance checks.
Optional `dependencies` and `approval` apply to migration preview. SQL checks also
run on the upgraded older fixture when rehearsing an upgrade. Use deterministic,
representative data and the matching workload/fixture when approving plan baselines.

Each stage must pass, report completed cleanup and match `current_schema` before
the next stage starts. Coverage consumes fresh verification and checked-plan reports
from this invocation, rather than user-supplied cached reports. Each stage's report
is nested in the consolidated JSON; failed and blocked stages identify release
blockers. Any failure returns exit code 1. Child output is bounded to 1 MiB per
stage. Inputs and the config are bounded regular files; invalid runtime inputs can
fail a stage before it produces a report, in which case cleanup is `unknown` rather
than claimed successful. Temporary evidence files are private and removed afterward.

Each stage has a ten-minute execution limit. Cancellation signals child commands
so disposable cleanup can finish, with up to four additional minutes before forced
termination. This command is a local release readiness check, not an attestation of
production performance, fixture completeness or test intent. Existing coverage
policy waivers and assertion semantics remain customer-controlled.

### Generate client migration guidance

`data-api migration guide` compares two saved contracts offline and presents
reviewable client updates without editing application files:

```sh
gregale --json data-api migration guide --baseline schema-before.json \
  --current schema-candidate.json --requests data-api.requests.json \
  --dependencies data-api.dependencies.json \
  --coverage-policy coverage-policy.json > migration-guide.json
```

Each change includes its compatibility classification, before/after normalized
contract shapes, suggested actions and applicable illustrative TypeScript examples.
Required insert fields, nullable reads, removed fields, changed write permissions,
RPC arguments/results and relationship changes receive targeted guidance. Enum and
composite changes expose their before/after shapes for review. Substitute actual
application values and helpers in examples; conversions, renames and replacement
fields are not inferred or applied. Regenerate database types and run client checks
against the candidate before releasing.

Optional declared dependency mappings identify matching saved-view/RPC consumers.
Saved request collections are read only for bounded name/method/path metadata;
requests are never executed. Any root-relation/RPC contract change flags matching
queries conservatively, regardless of whether a specific projection uses the changed
field. Embedded/computed lineage and transitive enum/composite usage are not inferred:
review those consumers or declare their dependencies. The guide omits request paths,
filter values, identities, payloads and response expectations from its output.

Coverage policies identify named checks to review for changed/new/removed relations;
shared enum/composite changes conservatively flag all inventoried relation checks.
Review data fixtures, integrity/access assertions, schema-bound evidence and budgets.
The guide does not rewrite coverage policies or approve breaking changes. Missing
optional manifests are explicitly marked `not_provided`, not interpreted as no impact.
Without `--json`, the same guidance is printed for human review.

A guide returns exit code 0 for valid analysis, including breaking changes; its
status is `needs_review` or `no_changes`. Use migration preview/release checks for
CI enforcement. Saved requests are bounded to 64 KiB/100 requests, dependencies to
1 MiB/100 consumers/1,000 declared paths, and guides to 1,000 contract changes.
Schema contracts and coverage policies retain their existing validation and bounds.

### Observe redacted query shapes

Set runtime environment variable `DATA_API_QUERY_SHAPES=1` to opt in to structural
query metadata in existing Data API request logs. Operation/query metadata is
omitted unless enabled. It is attached only after authentication/contract checks for known
catalog relations or RPCs with simple identifiers and a serving schema fingerprint.
Unknown paths, unsupported names, unauthenticated requests and oversized shapes
retain their normal request log without operation/query metadata.

Use existing diagnostics against hosted retained logs or saved NDJSON:

```sh
gregale --json data-api diagnostics --file requests.ndjson > diagnostics.json
gregale data-api diagnostics notes-data --since 15m --sort p95
gregale data-api diagnostics notes-data --since 15m --sort errors
```

Reports now include `operations` and `query_shapes`, with request/error counts,
error rates, gateway-latency p50/p95/p99 and up to three sample request IDs per shape.
Use the existing request-ID lookup to correlate an operation with a specific failure.
Shapes are grouped by schema fingerprint and operation. `--sort requests` (the
default) ranks by request count; `--sort p95` ranks by total gateway-request p95,
including aborted requests; `--sort errors` ranks by failure count, not error rate.
Ties use request count and stable shape identifiers. These rankings apply to
`query_shapes`, with the chosen order recorded in `query_shape_sort`.
Operation and shape statistics also include phase timings and their coverage.
Existing deployment/lookback filters let you compare samples across releases. These
are bounded retained-log summaries, not persistent metrics or complete traffic counts.
`shaped_requests` and `unshaped_data_requests` expose metadata coverage alongside
existing partial-sample/gap reporting. Per-operation statistics cover shaped requests.

A shape contains known catalog projection/filter/order fields, allowlisted operators,
ordering/null behavior, count mode and pagination-presence flags. Its ID hashes only
this structural description and method/resource, never query values. Limit values
are deliberately omitted, so different page sizes may share a shape. Claims, tokens,
identities, filters' operands, bodies, response values and raw request URLs are absent.
Unknown columns, JSON paths, embedded selects and logical expressions mark a shape
`complex` without copying caller-provided text. Mutations/RPC bodies are not inspected;
those operations can share a shape despite different payloads.

Use frequent, slow or failing shapes to choose representative saved requests for
`data-api explain`. Shape summaries cannot reconstruct executable production queries:
supply safe fixture values and review complex projections/filters yourself. The CLI
validates bounded metadata and its digest, discarding unrelated raw log fields; catalog
recognition happens in the runtime. Schema fingerprints identify serving contracts,
not deployment versions or database index changes. Use deployment filters to investigate
rollouts that retain the same schema. Gateway latency includes request/response work
and does not measure PostgreSQL execution time alone.

### Scaffold typed atomic business operations

Gregale RPCs already run each call in a PostgreSQL transaction and generate their
TypeScript arguments/results from the schema. Use the reusable transaction starter
for an editable tenant-scoped order/inventory example:

```sh
gregale data-api transaction init --name place_order --output transactions/place-order
```

The parent directory must exist. Existing output directories are never replaced.
The package contains a migration, synthetic fixture, client input parser, access
scenarios, integrity assertions and a review guide. No migration is applied by init.
Copy `migration.sql` into your next numbered project migration after review, then
regenerate database types. Place `client.ts` beside the generated `database.types.ts`;
it imports `Args` from `Database` rather than maintaining another interface. Wrap
`db.schema('api').rpc('place_order', args)` with the existing `validatedRpc` helper
and parser to preserve generated result inference and validate before networking.

The function creates an order header/items, locks stock rows in canonical SKU order,
checks availability, snapshots server-side prices and decrements inventory atomically.
A late failure rolls back all changes. A tenant/actor-scoped UUID request key identifies
retries: the same canonical payload returns the original receipt, while changed
quantities/SKUs conflict. Stock row locks prevent concurrent keys from overselling.
Do not delete receipts while retries remain possible. Example quantity, item-count
and price limits are business validation templates, not new platform quotas.

Ownership comes from verified `sub` and `tenant_id` claims, never RPC arguments.
The function is SECURITY INVOKER with a fixed search path, revoked PUBLIC execution
and the normal `@gregale:rpc` permission opt-in. Read policies expose tenant rows;
write policies additionally require the function's transaction-local scope marker,
blocking direct REST inserts/updates/deletes. Audit other exposed functions and SQL
grants: a scope marker does not defend against a role with arbitrary SQL access.
Adapt membership, admin and revocation semantics to your own application model.

For local examples configure Alice's tenant claim as `alpha` and Bob's as `beta`.
Load the fixture through the normal disposable development/verification tools;
manual fixture loading requires an owner transaction for its local claim/scope
settings. Run the generated `data-checks.json` and `access-checks.json` with
`data-api verify`, or include them in your release config. Access scenarios expect
fresh synthetic inventory and exercise a late rollback, successful commit, retry,
changed payload conflict, cross-tenant visibility and rejected direct mutations.
Existing required project authorization scenarios still run independently.

This starter needs review before production use. Database atomicity covers database
changes; use the existing transactional outbox workflow for external notifications.
It introduces no arbitrary-SQL endpoint, elevated definer function or SDK transaction
that bundles unrelated HTTP calls.

### Generate a v2 recoverable business transaction

Use the opt-in v2 package for new operations that need bounded replay and
read-only recovery:

```sh
gregale data-api transaction init --name place_order --contract v2 --output transactions/place-order-v2
```

It generates `api.place_order_v2`, `api.read_place_order_receipt_v2`, separate
`place_order_v2_*` tables, a typed preparation/recovery client, owner cleanup,
synthetic fixtures and verification checks. Default generation remains v1. V2
can coexist with the existing example; it does not migrate earlier business data.
Review the generated README and SQL, copy the migration into a new project
version, run owner RPC grants, refresh the runtime and regenerate types with
`--validators`. Install the paired SDK before adopting its client.

The sample operation locks inventory by ASCII SKU order, snapshots server prices,
and commits stock changes, the order/items and a private success receipt in one
transaction. A late shortage, RLS failure or receipt failure rolls back the
operation. Verified `tenant_id`/`sub` claims determine ownership. Business reads
are shared within a tenant; recovery requires the original tenant and actor.
Receipts survive later price changes and business-row deletion.

```ts
import { dataRequest } from '@gregale/data'
import { transactionClient, transactionMatches, reconcileMutation } from './client.js'

const transaction = transactionClient({
  url: dataAPIURL, accessToken: () => session.token,
  subject: session.subject, tenant: session.tenant,
})
const pending = transaction.prepare({ skus: ['a_widget'], quantities: [2] })
// Save pending before sending if it must survive a reload.
const response = await dataRequest(transaction.execute(pending), { timeoutMs: 5000 })
const result = reconcileMutation(response, transactionMatches(pending))

// Explicit read-only recovery after an uncertain response:
const lookup = await dataRequest(transaction.lookup(pending), { timeoutMs: 5000 })
const recovery = reconcileMutation(lookup, transactionMatches(pending))
```

Preparation validates before generating one UUIDv7 and freezes copied canonical
inputs with their API URL, optional schema fingerprint, tenant, actor and
contract. The client rejects changed context. It does not persist pending work.
For reload recovery retain that complete context and key; an optional second
argument to `prepare` restores the captured key after verifying context. Never
automatically replace uncertain keys or rebuild requests from current stock.
The runtime guard validates all returned lines, quantities, prices and total
before narrowing JSON into a typed original receipt.

V2 keys expire 24 hours after their embedded timestamp, with five minutes of
future clock tolerance. Reordering identical SKU/quantity pairs preserves payload
identity; changing them on a successful key gives `idempotency_payload_conflict`.
Writers refuse expired keys even after purge. Read-only lookup can confirm a
retained expired receipt; missing live receipts return zero rows and missing
expired receipts return `idempotency_key_expired`. Missing, conflicting and
expired results leave earlier uncertain operations unresolved. The clock,
database-history and owner-authority assumptions of the v2 replay contract apply.

The generated cleanup package pins `pg`; install its dependencies and retain the
lockfile. Use the owner migration binding through `MIGRATION_DATABASE_URL`,
preview a past UTC cutoff, then add `--apply` after reviewing the output:

```sh
npm run receipts:cleanup -- --before 2026-10-17T00:00:00.000Z --batch-size 100 --max-batches 1
```

Cleanup skips row/operation locks and purges only expired success snapshots. It
never deletes orders, items or inventory. Quantity/SKU/price bounds remain
editable application rules; membership, RLS, custom triggers, storage/retention
and outbox handling need business review. See
[ADR-1673](adr/1673-data-api-v2-transaction-generator.md).


### Receipt storage diagnostics

The starter and generated v2 transaction packages include an owner-only,
read-only receipt report. Use the secret-managed `MIGRATION_DATABASE_URL` that
assumes the stable receipt-table owner, after installing package dependencies:

```sh
# Starter: all three v2 note receipt contracts.
npm run --silent receipts:diagnostics
# Starter: select creation, update/delete, or batch receipts.
npm run --silent receipts:diagnostics -- --receipt-kind mutation
# Generated v2 transaction package: its own transaction receipt contract.
npm run --silent receipts:diagnostics
```

The single JSON object has `version: 1`, `status: "ok"` or `"incomplete"`, and
a `receipts` array. Successful entries include `contract`, `table`, `observedAt`,
`totalCount`, `activeCount`, `expiredCount`, `oldestExpiredAt`,
`oldestExpiredAgeMs`, `tableBytes`, `indexBytes` and `totalBytes`. Counts, ages
and byte sizes are decimal strings to preserve precision. Empty backlogs have
null oldest deadline and age. Expiry uses database statement time; retained
expired receipts still support recovery.

Unavailable entries have fixed `reason` values: `not_owner`, `missing_table`,
`unsupported_table`, `lock_timeout`, `statement_timeout`, `query_failed`,
`connection_failed` or `migration_binding_required`. They omit measurements.
Incomplete reports and invalid options exit nonzero. Successful contracts remain
visible when another fails. The serving login and all non-owners are refused,
including privileged non-owners. FORCE RLS, inherited/partitioned tables and
incompatible expiry columns are unsupported. This report covers v2 only.

Connection establishment and individual statements have 5-second deadlines;
lock waits have a 1-second limit and client queries a 6-second fallback. Exact
counts may time out on large tables. Counts share one snapshot per contract;
contracts have separate observation times. Physical sizes may change during a
report. Table bytes include TOAST and free allocation, and do not predict disk
reclaimed by deletion. Cleanup may leave physical allocation unchanged.

Keep these cross-user aggregates in operator monitoring. The command uses
read-only transactions and short ACCESS SHARE locks and publishes no receipt
contents. Existing cleanup remains explicit; expired backlog can include rows
that cleanup temporarily skips due to row or operation locks. See
[ADR-1675](adr/1675-data-api-receipt-storage-diagnostics.md).

### Durable pending-operation recovery

The optional `persistentTransactionClient` saves the complete original operation
before its first request. Select a dedicated IndexedDB database and a stable
application slot that you can find after reload (for example `checkout`). Install
the paired SDK and regenerate the v2 client to use this opt-in wrapper:

```ts
import { createIndexedDBMutationStore } from '@gregale/data'
import { persistentTransactionClient } from './client.js'

const store = createIndexedDBMutationStore({ databaseName: 'my-app-data-mutations' })
const transaction = persistentTransactionClient({
  url: dataAPIURL, subject: session.subject, tenant: session.tenant,
  accessToken: () => session.token, // Keep this provider scoped to this identity.
}, store)
const prepared = await transaction.prepare('checkout', { skus: ['a_widget'], quantities: [2] })
const sent = await transaction.execute(prepared, { timeoutMs: 5000 })
const checked = await transaction.reconcile(sent.entry, sent.response)
// A confirmed receipt can coexist with a failed local marker write.
if (checked.result.outcome === 'confirmed') showOriginalOrder(checked.result.row)
```

After reload, construct the same client/store and explicitly load the slot:

```ts
const pending = await transaction.load('checkout') // Sends no request.
if (pending) {
  const recovered = await transaction.recover(pending, { timeoutMs: 5000 })
  if (recovered.result.outcome === 'confirmed' && recovered.persistence === 'saved') {
    // Explicitly clear only after the application accepts this historical result.
    await transaction.removeConfirmed(recovered.entry)
  }
}
```

States are `prepared`, `uncertain` and `confirmed`. The first explicit execute
atomically persists `uncertain` before networking; one tab can claim a prepared
revision. Loading and recovery never replay writes. Failed storage writes or
acknowledgements prevent dispatch; uncertain responses keep the original key and
payload. A callback exception also preserves them, so load again for recovery.
Recreating a client requires loading a new handle rather than reusing an old one.

The original API URL, fingerprint (if pinned), contract, tenant, subject, key and
canonical input must match on restore. Changed context, corrupted/unknown records
and lossy parsers are refused without clearing storage. Missing or expired
receipts remain unresolved, including after the replay window closes. There is
no automatic eviction or new-key retry. `removeConfirmed` removes local state
only, and cannot remove an uncertain operation or a newer revision from another
tab. Use a new slot only for an explicitly new business operation.

Reconciliation returns `result`, `entry` and `persistence` (`saved`, `unchanged`,
`conflict` or `storage_failed`). Preserve a validated original receipt even when
saving its local marker fails; reload the slot to inspect current local state.
A confirmed marker does not describe current stock/order state or prove that a
later operation succeeded.

IndexedDB defaults to 100 records and 8 MiB of stored values. Each record is at
most 1 MiB, slot/database names 256 UTF-8 bytes, and JSON depth 64. Lower count/
byte budgets can be selected. Quota/unavailable storage fails closed before
sending and does not use volatile fallback. Payloads are intentionally stored;
credentials, headers and token providers are excluded from the generated
projection. Clearing/evicting origin storage or losing the device removes the
local copy. Close the store when finished. For other runtimes, the SDK's
`createMutationJournal` accepts an application-owned durable atomic CAS adapter;
a localStorage get/set pair cannot coordinate this protocol across tabs.

See [ADR-1674](adr/1674-data-api-durable-mutation-journal.md) for the persistence contract.

### Generated runtime input validators

Generate TypeScript types and typed insert/update parsers together:

```sh
gregale data-api types notes-data --validators --output client/src/database.types.ts
gregale data-api types notes-data --validators --output client/src/database.types.ts --check
```

The deployed Data API runtime must include validator generation. Older runtimes fail
without replacing the output file. `--validators` and `--snapshot` are mutually
exclusive. For a local runtime with its normal database configuration, the same
read-only generator is available as `node types.mjs --validators`.

```ts
import { inputValidators, DataAPIInputError } from './database.types.js'

try {
  const input = inputValidators.api.notes.insert(formValues)
  await client.from('notes').insert(input)
} catch (error) {
  if (error instanceof DataAPIInputError) showInvalidInput()
  else throw error
}
```

Parsers accept `unknown` and return the generated `Insert` or `Update` type. Insert
requires nonoptional writable fields; update allows omission. Both reject unknown
keys, generated/read-only columns, invalid nulls, explicit `undefined`, invalid
primitive and enum values, nonfinite numbers, cyclic values and object accessors.
Arrays and supported composites are checked recursively. Successful parsing returns
a detached JSON object; errors contain a stable `invalid_input` code and no input
values. Views have no write parsers. No dependency or HTTP request is needed.

Catalog metadata additionally supplies scalar `varchar(n)` limits in Unicode
code points and intrinsic `smallint`, `integer`, `bigint` and `oid` bounds. Bigint
inputs must be safe JavaScript integers; larger values need a separately reviewed
representation. There is no coercion or default insertion. Metadata and the schema
are read in one read-only transaction using the serving database binding.

Inspect `inputValidationWarnings` during review. Custom CHECK constraints, domain
rules and unsupported types are flagged rather than interpreted. Unsupported types
receive JSON-shape validation only; numeric precision/scale, character padding,
UUID/date formats, array dimensions and custom business rules remain database
checks. These validators are advisory: PostgreSQL constraints, triggers and RLS
remain authoritative, including races and tenant authorization. A validator file's
schema fingerprint identifies the existing API/type contract; extra catalog rules
do not alter that contract. Use `--validators --check` to detect changes to the
complete generated file, including those rules. Existing plain type generation
and contract snapshots keep their format.

For local development, opt in with `gregale data-api dev --validators`. Initial
startup and each successful migration reload regenerate types and parsers together
from one read-only catalog transaction, after compatibility and authorization
checks pass. Use the same flag in CI:

```sh
gregale data-api dev --validators --check --once
gregale data-api dev --validators --check-types --once
```

`--check-types` compares the entire generated module, including catalog-only
validation rules, and preserves a stale file on failure. Plain generation remains
the default; keep the generation mode consistent across development and CI.
The notes starter form uses available generated insert validators after normalizing
its fields and before creating its idempotency key, reading a token or sending a
request. Schema violations show field feedback and leave no pending submission.
Its RPC business rules and database authorization still apply. Without generated
validators, the starter retains its existing form validation.

Generated `DataAPIInputError` also exposes a readonly `issues` array. Parsers stop
at the first failure, so the array contains one immutable issue:

```ts
try {
  inputValidators.api.notes.insert(formValues)
} catch (error) {
  if (error instanceof DataAPIInputError) {
    // Example: { code: 'too_long', path: ['body'] }
    showFieldFeedback(error.issues[0])
  }
}
```

`path` is an array of known schema field names and array indices. Nested composites
can produce paths such as `['address', 'city']`, and typed arrays can produce
`['tags', 1]`. Root failures use `[]`. Unknown fields report their parent path,
and arbitrary JSON property names are omitted from paths; neither names supplied
outside the schema nor submitted values appear in generated errors. The top-level
error code remains `invalid_input` and its message remains generic.

Stable issue codes are `required`, `null_not_allowed`, `invalid_type`,
`invalid_enum`, `not_finite`, `invalid_integer`, `out_of_range`, `too_long`,
`unknown_field`, `invalid_json`, `cyclic_input`, `input_too_complex` and
`unsupported_object`. Use these codes for application-owned or localized messages.
The notes starter maps body issues to field feedback, including “This note is too
long.” It retains generic feedback for rules without a dedicated message. Regenerate
validator modules to receive the new issue format; plain types remain supported.

### Generated RPC argument parsers

The same `types --validators` and `dev --validators` output now includes a separate
`rpcValidators` map for exposed functions. Each parser returns that function's
schema-generated `Args`, and implements the SDK parser interface:

```ts
import { validatedRpc } from '@gregale/data'
import { rpcValidators } from './database.types.js'

const outcome = await validatedRpc(
  untrustedInput,
  rpcValidators.api.place_order,
  args => client.schema('api').rpc('place_order', args),
)
```

Only already-exposed, supported functions in the normalized contract receive a
parser; this adds no function grants or API routes. Table and RPC validator maps
are separate, so identically named relations and functions remain unambiguous.
Parsers require named arguments without defaults and preserve omitted defaulted
arguments for PostgreSQL to fill. They never export or evaluate default SQL.
Explicit null remains permitted, matching generated RPC argument types; the
function may impose stricter rules. Explicit `undefined` and unknown keys fail.
Enums, primitives, arrays, composites and supported table-row arguments are checked
recursively, and successful results are detached JSON objects.

Direct `.parse(input)` failures expose the same immutable field issues as table
parsers. `validatedRpc` retains its existing redacted failure result and does not
construct the query when parsing fails; call `.parse` separately when a form needs
field feedback. Result inference is preserved by the query callback.

Every RPC exports a `rpc_database_rules_require_review` warning: its argument
contract does not describe function-body rules, domain constraints, UUID/date
formats, integer widths, array dimensions or cross-argument conditions. Defaults
can be omitted but cannot be inferred client-side. The transaction starter uses
the generated parser for argument names/presence/shapes, and keeps a small wrapper
for its UUID, SKU, quantity and retry-key business rules. SQL and RLS remain
responsible for atomicity, concurrency and authorization.

### Batch validation before imports

Validator output includes `validateInputRows`, which accepts up to 1,000 rows and a
synchronous generated parser:

```ts
import { inputValidators, validateInputRows } from './database.types.js'
const preview = validateInputRows(mappedRows, inputValidators.api.notes.insert)
if (!preview.ok) {
  // Example: { row: 12, code: 'invalid_integer', path: ['priority'] }
  showImportErrors(preview.errors, preview.invalid, preview.truncated)
} else {
  // Present this detached payload for review before explicitly submitting it.
  showImportApproval(preview.rows)
}
```

Every row is checked, even after the report cap is reached. Counts include all
rows; errors include the first failure per invalid row, with one-based row numbers,
known field paths and stable reason codes. The default report cap is 100; set
`maxErrors` from 1 to 1,000 when needed. A failed batch has no `rows` property, so
there is no partial payload to accidentally submit. Custom parser exceptions are
redacted to `invalid_input`; parser messages and supplied values are never copied.
Use trusted synchronous generated parsers; asynchronous parsers are rejected.
Successful generated parsing produces detached objects and a readonly frozen array.
Editing those objects requires a fresh preview before approval.

The team CSV preview applies generated `team_orders` insert rules when validator
output and signed-in identity context are available, after its existing CSV and
business checks. Tenant and creator come from the application's selected team and
membership. Reports show at most 100 errors and complete valid/invalid counts;
any generated schema failure withholds the mapped import payload. The existing
approval and server-side transactional import checks remain in place.

The notes starter includes `previewNotesCsv` in `client/src/note-import.ts`:

```ts
const preview = previewNotesCsv(csv, { subject: signedInSubject })
```

It requires generated validators, accepts mapped body and optional priority columns,
and supplies subject from application context. Ownership columns in CSV are ignored.
Numeric conversion is explicit for decimal CSV priority values; empty priority cells
are omitted for the database default. String values are preserved. CSV input is
bounded to 1 MiB and 1,000 data records. Reports use CSV record numbers including
the header (first data record is 2), not physical line numbers for multiline fields.
Neither helper retrieves tokens, constructs requests nor writes to the database.
Database constraints, triggers and RLS still enforce rules at submission time.

### Schema-driven form metadata

`types --validators` and `dev --validators` also export
`formMetadata.<schema>.<table>.insert` and `.update`. Arrays describe writable
fields only: their names, suggested control (`text`, `number`, `checkbox`, `select`
or `custom`), required presence, nullability, enum options, supported varchar
lengths in Unicode code points, intrinsic numeric bounds/steps and review flags.
Views and generated/read-only columns have no editable metadata. Update fields
are optional. Defaults are never read or evaluated client-side.

```ts
const fields = formMetadata.api.notes.insert.map(field => ({
  ...field,
  label: labels[field.name] ?? field.name,
}))
```

Keep labels, layout, visibility, ownership context and business rules in application
code. Copy metadata for application overrides instead of editing generated files.
Use an explicit editable-field allowlist; a writable column may still be an ownership
or system field. `custom` controls and `requiresReview` fields need application
review; suggestions do not describe every database constraint. Do not infer
nullable values or default expressions from blank strings.

The notes browser starter builds its create body field and edit body/priority fields
from this metadata, retaining hand-authored tag entry and transaction behavior.
Ownership, IDs and versions stay outside the generated editor. Plain type output
uses the starter's existing field defaults. After regeneration, forms receive new
enum choices and supported limits without duplicate schema constants. Browser
length guidance uses code points; it does not set HTML `maxlength`, whose UTF-16
count differs. Generated validators enforce the limit and provide field feedback.

Edit uses a note ID and version from a fresh read; blank editable fields are omitted,
not cleared. Generated update validation runs before token lookup or transport,
then the existing version-checked update runs once with a five-second deadline.
Conflicts require a fresh read. An uncertain save is never automatically replayed.
SQL constraints and RLS remain authoritative.

### Local table browser and reviewed inserts

The local inspector printed by `data-api dev` now includes a generic table browser.
Select an exposed table or view and a test identity, then read visible rows using
known readable columns. Filters use a selected scalar column and supported operator;
pages show up to 50 rows with a one-row lookahead. Offset pages reflect current
RLS visibility and are not a stable snapshot; refresh after concurrent changes.
Unusual/reserved column names remain available in the request explorer rather than
being guessed as filter syntax. RPCs remain in that explorer.

For tables, the inspector builds insert fields from the same bounded schema/catalog
rules as generated validators, even when types are generated without `--validators`.
Choose omitted/default, explicit value or NULL as permitted. Complex values use
JSON entry and require review; default SQL and domain/CHECK expressions are never
exported or evaluated. Required/enum/type/length/integer checks run locally and show
field issues before any request. Models exceeding limits disable insert forms while
leaving reads and the request explorer available.

Validate and review the exact payload and selected identity, then explicitly approve
and confirm once. Changing fields, identity, table, schema or catalog rules invalidates
the review. Confirmed inserts use the schema-fingerprint route, the selected local
JWT, the appropriate schema profile and `return=minimal`; PostgreSQL grants, rules
and RLS still decide whether insertion is allowed. The inspector never writes using
migration-owner credentials, auto-retries mutations or stores reviewed payloads in
project receipts. If the response is lost, read the table before deciding on another
submission. The inspector remains loopback-only with its existing private capability,
origin/host checks, CSP and no-store policy. Rows and labels render as text.

This first table-browser increment supports reads and inserts. Row editing and deletion
remain in the request explorer until a reliable key and concurrency policy can be
established from the schema; the browser does not infer them from an `id` column.

### Row keys and version-checked inspector edits

Current introspection emits snapshot version 4, adding optional `primary_key` and
`concurrency` metadata on base tables. Versions 1–3 remain valid baselines. A complete,
readable primary key is exported with its ordered columns, including composite keys;
`filterable` identifies conservative scalar representations supported by the editor.
Keys containing unreadable columns are omitted. Views do not acquire inferred keys.
Plain generated types also export `tableMetadata.<schema>.<table>`.

A version column requires an explicit owner declaration in a new migration:

```sql
COMMENT ON COLUMN api.notes.version IS '@gregale:version';
```

Declare this only when the database manages a nonnullable integer and advances it
on every successful update. Introspection requires exactly one such readable integer
column and an enabled, unconditional row-level BEFORE UPDATE trigger covering all
updates. It does not read or prove the trigger's function body. The owner declaration
is a contract about that behavior; a name such as `version` alone is insufficient.
The notes starter includes `migrations/examples/enable-versioned-edits.sql` to copy
into a new numbered migration after reviewing its existing version trigger. Existing
applied migrations are not changed automatically.

For a supported primary key and declared version, the inspector shows an Edit action
on visible rows. It excludes key/version fields, validates the selected patch, shows
before/after values, and requires explicit approval. The request filters every key
column and the reviewed version, uses the selected test JWT and schema-fingerprint
route, and asks for the updated row. Empty results mean the row changed, disappeared
or is unavailable; read it again. An unexpected key/version response requires review
of the database declaration. Only one matching row with an advanced safe integer
version confirms the edit. No automatic retry occurs, and a fresh read is required
before another edit. RLS still controls reads and mutations.

The editor declines unsupported key types, unusual/reserved filter names, unsafe
JavaScript integer values and undeclared concurrency. These tables remain browsable
and keep their request-explorer access. Snapshot/key metadata contributes to the
schema fingerprint; regenerate types/contracts and review existing accepted-contract
policies during rollout. Adding metadata is compatible; changing/removing an existing
key or version declaration is reported as breaking for consumers using those rules.

The local Data API inspector offers Parent and Children actions for exposed foreign keys, including composite keys. Each action reads related rows with every key component under the selected identity and database RLS. Nullable, incomplete, unsupported or unsafe keys have no navigation action. The Back trail restores filters and pagination and re-fetches rows; changing identity or schema clears it. Navigation sends reads only and does not populate write forms.

### Saved relationship views in the local inspector

Choose visible columns, a scalar filter and sorting in **Browse rows**, then
follow Parent or Children actions to explore a relationship path. In **Saved
relationship views**, give the workflow a name and select **Save current path**.
The inspector writes `data-api.views.json` beside `data-api.requests.json` in the
project using atomic saves and revision checks. Reload the file after another
editor changes it before saving again.

**Open under selected identity** starts with fresh rows from the first table.
Choose a visible row and its **Continue saved view** action at each step. The
view restores each step's columns, filter and sorting, while deriving composite
key predicates from the newly selected row. Back re-fetches the previous step.
It does not replay earlier row keys or perform writes. The selected identity and
PostgreSQL RLS govern every read; changing identity clears the in-memory path.

Definitions store table/column choices, explicit filter values, sorting, named
foreign keys and the schema fingerprint. They exclude credentials, identity
names, row results, clicked-row keys and page offsets. Review explicit filter
values before committing this file to Git. Visible-column selection controls
the grid; readable row fields remain available to editing and navigation.
Collections are limited to 100 named views and 64 KiB; each path has at most
33 steps. Views saved against a different schema fingerprint are marked **needs
review** and cannot open until you reconstruct, review and save an updated path.
Reads of an opened saved view also use the pinned contract route so a schema
change between session refreshes refuses the read.

### Guided RLS checks from a saved relationship view

In the local inspector, select a saved view and open **Guided RLS checks**.
**Generate read checks from selected saved view** creates a version 3 access-check scaffold
with a named fixture for each relationship step and an expectation for every
configured identity. The scaffold sets `status` to `needs_review` and leaves
fixture keys and row counts empty; it does not infer ownership or copy clicked
row keys, response contents, token claims or credentials.

Supply synthetic fixture keys matching rows in your SQL fixture or local seed.
Include every declared filterable primary-key column and every column needed
by the relationship on either side, including all composite-key components.
Give each identity an exact expected row count for each step (`0` means hidden,
`1` means one visible fixture row), then set `status` to `reviewed`. Every step
requires at least one positive expectation so missing seed data cannot make an
all-denied check suite pass. Counts are bounded to 0–50, with at most 100 identity
and step checks across a manifest.

For example, a notes/comments workflow might use these named bindings and
expectations within its generated manifest:

```json
{
  "fixtures": [
    { "name": "step_1", "schema": "api", "table": "notes", "keys": { "id": 101, "subject": "alice" } },
    { "name": "step_2", "schema": "api", "table": "comments", "keys": { "id": 201, "subject": "alice", "note_id": 101 } }
  ],
  "checks": [
    { "identity": "alice", "steps": [{ "fixture": "step_1", "rows": 1 }, { "fixture": "step_2", "rows": 1 }] },
    { "identity": "bob", "steps": [{ "fixture": "step_1", "rows": 0 }, { "fixture": "step_2", "rows": 0 }] }
  ]
}
```

Keep the generated `version`, `status` and `view` fields around this excerpt.
**Save checks** writes the complete manifest to `data-api.access.json`, using
revision checks to prevent stale overwrites. **Run saved checks** performs
read-only GET assertions against the current seeded database. Every identity
is tested at every relationship step, including child rows when that identity
cannot see the parent. Named fixture keys scope each query, and saved view
filters also apply. Visible-column selection and sorting do not change counts.
Results identify the identity, fixture and relationship step and show counts,
excluding response data, fixture values and credentials.

Run the same manifest in CI against a disposable migrated database:

```sh
gregale data-api verify --directory . \
  --access-checks data-api.access.json --fixture fixture.sql --json
```

The existing verifier supplies fresh local identity tokens, loads the synthetic
SQL fixture, checks project authorization, runs the guided assertions and cleans
up disposable services. A failed expectation, missing fixture, unreviewed
scaffold, stale schema fingerprint or failed read exits nonzero. Legacy version
1/2 request-based access checks continue to work. Guided view checks cover read
visibility; retain explicit request scenarios for mutation, admin and membership
revocation checks.

### Guided mutation checks in disposable verification

Select a saved view and choose **Export mutation scaffold for CLI** in the
inspector's Guided RLS checks panel. The downloaded `data-api.mutations.json`
is an unreviewed version 4 manifest. It retains the version 3 read-baseline
fields and adds `mutationFixtures` and ordered `mutations`. It leaves payloads,
fixture selectors and write expectations for review. The inspector continues
to save and execute version 3 read checks only; mutation files run through the
CLI against its disposable database.

Review the read fixtures and expectations first. Mutation fixtures name
additional synthetic rows, for example a row that should be absent before a
create. Every create payload must include those selector values. Updates and
deletes must include the full declared filterable primary key in their fixture
binding. Each operation specifies an identity, fixture, method, explicit body
(`null` for DELETE) and an expectation:

```json
{
  "name": "Bob_update_denied",
  "identity": "bob",
  "fixture": "step_1",
  "method": "PATCH",
  "body": { "body": "synthetic-denied-edit" },
  "expect": { "status": 204, "rows": 0, "outcome": "unchanged" }
}
```

Allowed create/update/delete expectations use outcomes `created`, `updated`
and `deleted`, respectively, with `rows: 1`. Create expects HTTP 201; update and
delete expect HTTP 204. Denied writes use `outcome: "unchanged"`, `rows: 0`
and either HTTP 204 for a filtered-out update/delete, or HTTP 403 with PostgreSQL
permission code `42501` for an explicit RLS refusal. Denied creates require the
403 permission refusal. Authentication errors and unrelated constraint failures
cannot satisfy a denial expectation.

Set `status` to `reviewed`, seed the named synthetic rows in `fixture.sql`, then
run:

```sh
gregale data-api verify --directory . \
  --access-checks data-api.mutations.json --fixture fixture.sql --json
```

Read baselines must pass before any mutation executes. Operations run in manifest
order against the disposable database and use `Prefer: return=minimal`, so an
ownership transfer cannot appear denied just because its returned row becomes
unreadable. The verifier uses a separate owner connection in read-only,
repeatable-read transactions to observe all physical tables exposed by the
contract before and after writes. Denial expectations require identical table
row state, including fields hidden from the acting identity; sequence counters
are outside that comparison. Allowed writes require the expected fixture
cardinality and payload values after the operation. Updates must attempt an
actual change, rather than passing as a no-op.

The observer bounds synthetic datasets to 64 physical tables, 1,000 rows per
table, 64 KiB per serialized row and 1 MiB of total serialized row state.
Tables outside the exposed contract are outside this check. State remains
private to the verifier; reports contain operation/identity names and boolean
state assertions, excluding fixture values, row contents and credentials.
A failed or uncertain write stops later mutations without retrying. Successful
mutations remain in the disposable database until normal verifier cleanup;
they do not run against the inspector database or a hosted endpoint.

### Authorization lifecycle checkpoints

Choose **Export authorization lifecycle scaffold for CLI** in the inspector to
create `data-api.lifecycle.json`. This version 5 manifest extends the disposable
mutation format with ordered GET checkpoints between writes. The scaffold
contains a before-change read, an authorization change, member read/write checks
and an unaffected-member read. Identities, membership table/selectors, payloads
and expectations remain empty for explicit review. Choose DELETE to test
membership removal or PATCH with a role payload to test a downgrade. No admin
identity, membership model or permission outcome is inferred.

The version 3 read-baseline fields and version 4 write expectations still apply.
Version 5 requires at least one write and one read checkpoint in its ordered
`mutations` list. A read checkpoint has a null body and HTTP 200 expectation,
with `outcome: "visible"` and an exact row count of `0` or `1`:

```json
{
  "name": "Former_member_cannot_read",
  "identity": "member",
  "fixture": "step_1",
  "method": "GET",
  "body": null,
  "expect": { "status": 200, "rows": 0, "outcome": "visible" }
}
```

Supply the authorization row and unaffected member's data as named
`mutationFixtures`. Keep the data fixture itself present when revoking membership:
checkpoints require exactly one matching row through the private observer both
before and after the read, even when the acting identity must see zero rows.
Deleting the data cannot make a permission denial checkpoint pass. Checkpoint
queries use the named fixture selectors rather than saved-view filters or
sorting, and require unchanged exposed-table row state. They do not perform
writes. Version 4 continues to reject GET steps.

For a downgrade, check that the member can still read but can no longer update,
then verify that an unaffected member retains access. For revocation, check
that reads and writes are denied using the same named data fixture, and check
another member again. The verifier issues identity tokens before the baseline
and reuses those exact tokens throughout the workflow; it does not refresh
claims after an authorization change. This exposes policies that continue to
trust stale role or membership claims instead of current authorization state.
Expectations remain customer-defined because some products intentionally retain
permissions until token expiry.

Run the reviewed scaffold with your synthetic seed or an explicit SQL fixture:

```sh
gregale data-api verify --directory . \
  --access-checks data-api.lifecycle.json --fixture fixture.sql --json
```

Results identify each operation, identity and fixture, with checkpoint row counts
and state assertions. Row contents and JWTs remain excluded. Any failed checkpoint
or failed/uncertain mutation skips the remaining workflow; disposable-service
cleanup still runs. Lifecycle manifests retain the 64 KiB collection and 100
baseline-plus-operation limits and the mutation observer's dataset bounds.
Inspector save/run endpoints remain version 3 and read-only; exported version
4/5 workflows execute only through disposable CLI verification.

### Schema-aware CSV imports in the local inspector

Select a writable table and local test identity, then open **Import CSV into the
selected table**. Choose a UTF-8 CSV file or paste CSV text and select **Load
headers**. The parser supports a BOM, CRLF, quoted commas, escaped quotes and
multiline quoted cells. Limits are 1 MiB, 1,000 data records and 256 CSV columns.
Record numbers in previews and reports include the header as record 1; multiline
cells do not create additional records.

Each header maps to one writable database column or **Ignore this CSV column**.
Exact writable names are mapped initially; generated/readonly fields such as
identity columns remain ignored. Choose blank-cell handling separately for each
mapped column: preserve the value (including an empty string), omit it to use a
database default, or send SQL NULL. Nullability and required fields are still
validated. Strings and enums preserve their data-cell contents; numbers use
strict decimal notation, booleans accept true/false, and arrays, composites and
JSON use JSON cell values. Supply ownership and foreign-key values explicitly;
relationship navigation does not populate CSV payloads.

**Validate and preview** converts cells and runs the same server-generated input
model used by the inspector's insert form. It checks supported scalar types,
enums, Unicode length, integer ranges, nullability and structured input limits.
Any invalid record blocks the entire import before sending writes. Errors show
record numbers and known field paths without input values; the first 100 errors
are retained while full valid/invalid counts are reported. A serialized row must
fit the 64 KiB batch limit. Database CHECK/domain rules, triggers and RLS remain
final database checks.

The preview shows exact insert payloads in pages of 50 records, with the selected
table, identity and total count. Select the approval checkbox, then **Import
approved rows**. Changes to CSV, mappings, identity, table or schema invalidate
approval. Imports use the pinned contract route with the approved identity;
source and mapping controls are disabled during a run. **Stop import** cancels
the active request and prevents later batches.

Each request contains at most 25 rows and 64 KiB of JSON. Batches commit
independently; a confirmed transaction refusal means none of that batch's rows
were inserted. Later batches can still succeed. A batch is confirmed only after
HTTP 201 with an exact affected-row count matching the submitted count. Missing
or mismatched counts, including trigger-suppressed inserts, are uncertain rather
than falsely reported as imported. Timeouts, network errors and unrecognized
responses also mark the active batch uncertain and stop later requests. No
automatic retry occurs.

Reports label each original CSV record `confirmed`, `refused`, `uncertain` or
`not_sent`, with HTTP statuses where available. **Preview refused or unsent rows**
creates a new approval preview from the same validated payloads; confirmed and
uncertain records are excluded. Changing data or mappings starts a new import,
so prepare its CSV using the prior report to avoid repeating earlier inserts.
Read the table before deciding whether to import uncertain records manually.
**Download outcome report** exports table/identity names, schema fingerprint,
record outcomes and counts, excluding CSV values and credentials. CSV payloads
and retry state remain in browser memory and are not saved to request/view
collections; approved records are persisted by the Data API as normal inserts.

### Export inspector views to CSV

The inspector exports the selected columns under the selected identity and RLS,
including current filters and relationship predicates. Reopen a saved view to
reuse its projection and sort before exporting. Export starts at the first page,
adds readable primary-key columns as sorting tie breakers, and reads beyond the
visible page without adding those keys to the downloaded projection.

Choose a row limit (default 1,000; maximum 10,000). Progress reports rows and
bytes; one extra row detects whether the row limit truncated the result. Exports
are bounded to 10 MiB, with bounded response pages and timeouts. Stop, a changed
context, or a failed read cancels the download; no partial file is offered.
These are live ordered OFFSET reads, not a database snapshot: concurrent writes
can shift subsequent pages, and relations without unique keys can have sorting
ties. Use narrower filters when a stable result is required.

Spreadsheet mode protects formula-like text with a leading apostrophe and emits
NULL as an empty cell. Data-preserving mode instead JSON-encodes each cell,
distinguishing NULL, empty strings, booleans, arrays, and objects. It preserves
numeric lexemes, including PostgreSQL integers and decimals beyond JavaScript
precision. Select **JSON-encoded cells** in the import wizard to preview that
format for re-import. Values that cannot be represented exactly by the current
JavaScript input model are rejected with `precision_loss`; readonly columns
remain unmapped and inserts still require review and database authorization.

The existing import bounds still apply (1 MiB and 1,000 records per preview);
split larger exports into smaller files before re-importing them.

### Bulk updates and deletes in the inspector

Select rows using the checkboxes beside their actions, then choose Update or
Delete. Selection covers at most 50 rows on the current page. Rows need a
readable, filterable primary key and declared integer version, as single-row
edits do. For updates, enter a JSON object using the listed writable fields;
keys and the version field cannot be changed. Preview shows each key, reviewed
version, and before/after values, or an explicit deletion, under the selected
identity. Approval applies only to that exact preview.

Each row runs independently through the contract-pinned REST API with key and
version predicates and normal database authorization/RLS. Returned keys and
versions must match before an operation is confirmed. Zero affected rows are
conflicts: the row changed, disappeared, or became unavailable. Read fresh rows
before reviewing those again. Failed or cancelled requests whose commit cannot
be established are uncertain and stop subsequent writes. Responses and elapsed
time are bounded; no automatic retry occurs.

Results show row indexes, HTTP statuses, and confirmed/refused/conflict/uncertain/
unsent outcomes without row contents or credentials. The retry button creates a
new preview only for refused or unsent rows, retaining their original key/version
snapshots and requiring fresh approval. Confirmed, conflicting, and uncertain
operations are excluded. Changing the identity, table, view, filters, selection,
or changes invalidates approval; re-reading or changing the browsing context
clears the selection. Selected-row mode operates on the current page. Matching-view mode is described
below. Neither mode is a multi-row transaction.


### Apply bulk writes to a matching view

Choose **All rows matching this view**, select a preview limit from 1 to 1,000,
and validate the update or deletion. The inspector reads ordered pages through
the contract-pinned API under the selected identity/RLS, preserving the current
filter and relationship predicates. It fetches primary keys, declared versions,
and changed columns for the before/after preview. Sorting uses primary-key tie
breakers. Saved views can supply the browsing context as usual.

The complete matching result must fit within the chosen limit. An extra matching
row blocks approval; increase the limit or narrow the view rather than approving
a partial result. Zero matches, failed reads, malformed rows, duplicate keys,
changed context, and cancellation also produce no approved operation. Reads
are bounded to 1 MiB per response and 5 MiB per preview, with timeouts and progress.

Preview displays the affected count and exact reviewed keys, versions, and
changes. Approval freezes that list. Execution uses individual key/version
predicates, never a freshly evaluated broad filter. Rows that start matching
later are excluded; concurrent changes to reviewed rows produce conflicts. The
reads are live OFFSET pages, not a consistent database snapshot, so concurrent
writes can shift page membership during collection. Inspect the reviewed list
before approval. Writes retain independent commits, per-row progress,
cancellation, and reviewed retries for refused or unsent original snapshots.

### Download bulk-operation reports

After a selected-row or matching-view run, **Download last operation report**
saves `data-api.bulk-report.json`. Version 1 reports include a random run ID,
operation and source, schema/table, identity name, schema fingerprint, start and
finish timestamps, termination state, total count, summary counts, and every
reviewed row's outcome and HTTP status when available. Cancelled and stopped
runs include unsent rows rather than omitting them.

Separate reconciliation lists contain conflicting and uncertain row indexes.
The retryable list contains only refused or unsent indexes. Reports exclude
primary keys, versions, before/after values, filters, relationship values,
request/response bodies, JWTs, and session credentials. Indexes refer to the
reviewed preview; the report alone cannot identify records or reconstruct writes.
Use the original preview to reconcile those indexes with records.

A retry still uses the current in-memory approved snapshots, requires a new
preview and approval, and excludes confirmed, conflicting, and uncertain rows.
Its preview and report link to the immediate prior run ID and original row
indexes (`retry_of` and `source_index`). Download each report to retain the chain.
Reports are not executable manifests and cannot be imported to replay writes.

The latest finished report remains downloadable across changes of identity,
view, filters, or table. A cancelled or failed matching-read preview does not
replace it. The operation history below persists redacted reports in the project. Write
snapshots remain in the current tab only; download reports for an independent
durable copy. Nothing is uploaded to a hosted service.

### Preflight bulk writes before approval

After reviewing selected or matching rows, choose **Check readiness** before
approving. Read-only preflight checks that the schema fingerprint and known
writable fields still match, and reads each reviewed key under the selected
identity/RLS to verify visibility and the expected integer version. It fetches
only keys and versions through the pinned contract API. Changed rows and
unavailable rows are reported separately; unavailable covers both hidden and
deleted records without distinguishing them.

The readiness summary lists row indexes and check statuses without row contents
or credentials. Approval is enabled only after every reviewed row is ready, and
checks expire after 60 seconds. Expiration clears approval and requires checking
again. Any failed, malformed, refused, cancelled, or context-invalid check clears
the preview and requires a fresh preview. In selected-row mode, read fresh rows
before selecting and previewing again. Checks never refresh reviewed versions
silently, never perform writes, and do not replace the latest operation report.
Requests, response bytes, and elapsed time are bounded, with progress and Stop.

Readiness validates current reads and metadata; it cannot prove UPDATE/DELETE
policies, constraints, or trigger behavior without executing the write. Data can
change after a check. Execution retains contract pinning, individual version
guards, independent commits, and per-row outcomes, so a later concurrent edit
still produces a conflict rather than being overwritten. Retry previews require
the same readiness checks and new approval.

### Reconcile conflicting and uncertain bulk writes

Choose **Read unresolved rows** after a run to re-read conflicting and uncertain
keys under the original identity/RLS and pinned schema. The inspector displays
reviewed and current versions, and reviewed, desired, and current field values.
It labels visible rows as current, changed, or potentially applied when an
update's requested values currently match. Missing or hidden rows are unavailable.
Read errors and unchecked rows are displayed without becoming selectable.
Reads have per-response and total retained-data bounds, timeouts, progress, and Stop.

Reconciliation never changes the original outcomes or operation report.
Matching values may have come from another writer; unavailable rows do not prove
that a deletion committed. An uncertain operation therefore remains uncertain.
The ordinary retry button continues to exclude both conflicts and uncertain rows.

Select visible reconciled rows to **Preview a new operation** with their freshly
read versions. Selecting an uncertain row requires acknowledging that its earlier
write may already have committed and a new write may run triggers again. This is
a separate operation, not an automatic retry. Review its current before/after
values, run readiness checks, and explicitly approve it. Reports link to the
original run with `reconciliation_of` and `source_index`, excluding row contents
and credentials as usual. Changes after reconciliation remain protected by the
new version predicates and preflight checks.

Reconciliation uses the current in-memory run snapshots, not imported reports.
Changing identity, table, schema, filters, operation, or changes clears the active
snapshot. Available session snapshots may be reopened from operation history
under the original identity and matching schema. A failed schema or metadata check requires reading fresh rows
and creating a new preview independently. Cancellation discards the reconciliation
read; no follow-up operation is approved or sent.

### Bulk-operation history and retention

The inspector keeps redacted operation reports in `data-api.bulk-history.json`
next to the saved views and requests. History is bounded to 20 runs and 2 MiB,
with a default retention of 30 days. Adjust the run count (1–20) and age (1–365
days), then apply retention to remove older reports. Age-expired reports are
hidden when loaded and pruned from the file on the next save. Complete reports
are retained or removed as a whole; individual outcome lists are never truncated.
Saving uses revision checks, atomic replacement, and private file permissions.
Concurrent changes require reloading before explicit retention or clearing;
automatic report appends reload and merge once on a revision conflict.

Select a run to view counts, timestamps, termination state, and retry or
reconciliation links, and download its report. Links can point to a run removed
by retention; the inspector indicates whether the parent report remains present.
The server rejects report fields outside the redacted schema, including row keys,
values, write payloads, read predicates, and credentials. A failed history save
does not alter database outcomes; download the last report to retain a copy.

Up to 20 run snapshots and 10 MiB of keys, reviewed values, and browsing context
remain in the current inspector tab only. Reopen an available snapshot to restore
its original identity, table, and view and perform fresh reconciliation reads or
review an eligible retry. Reopening sends no writes and restores no approval.
Schema, writable-model, and identity-subject changes prevent restoring an old
snapshot. Follow-up operations still require fresh previews, preflight, and
approval. Snapshot eviction leaves the redacted report downloadable.

After restarting or reopening the inspector, project reports remain available,
but snapshots do not: reports cannot reconstruct keys or replay writes. Clearing
history requires the explicit clear checkbox and removes stored reports and
current-tab snapshots. It does not modify application records. Other already-open
tabs clear their retained snapshots when they reload the changed history.

### Read-only support views

Support identities can browse focused saved views, filter rows (including
case-insensitive `ilike` text matching with `*term*` wildcards), navigate exposed
relationships, and export the same identity/RLS-visible projection to CSV.
The inspector disables application write forms, imports, bulk writes and
mutating request methods for these identities.

In `data-api.dev.json`, set `read_only: true` on a local identity. Local token
issuance adds the reserved signed claim `gregale_read_only: true`; custom claims
cannot override it. For deployed applications, configure your trusted JWT issuer
to include that boolean claim on support tokens. The runtime verifies the token
and rejects POST, PATCH and DELETE before forwarding, including opted-in RPC
POSTs. Non-boolean values are rejected as invalid tokens. GET/HEAD continue through
normal database permissions and RLS. The SQL role remains the binding login:
external role claims cannot choose an owner, administrator, or another binding.

To save a support view, select a configured read-only identity and choose **Save
as a read-only support view**. Its definition retains the identity name, selected
columns, filters, sorting and relationship path, excluding subjects and credentials.
Reopening chooses that read-only identity and rejects missing or writable identity
configurations. Schema changes still require reviewing and resaving the view.
The mode and projection organize the UI; they are not authorization boundaries.

For database-backed restrictions, the starter includes
`migrations/examples/support/support.sql` and its README. Copy the example into
a new numbered migration and adapt it deliberately. It creates private support
cases/replies with sensitive columns, grants the binding role SELECT only on safe
base columns, denies base writes, and applies tenant/claim-aware SELECT RLS.
Security-invoker, security-barrier views expose the safe columns in `api` while
preserving base grants and RLS. Keep the private schema out of `DATA_API_SCHEMAS`.
Readable private base columns can supply complete FK lineage between exposed
views; private relations and unreadable columns never enter generated API types.

Do not grant broad access to the private base tables or expose functions that
return sensitive fields. Use a separate restricted binding/API when existing
base grants or exposed resources cannot satisfy support restrictions. Test the
intended support identities against safe reads, tenant isolation, hidden-column
requests and write rejections before exposing customer data. The local inspector
capability grants all local test identities; it is a development tool, not a
production support-staff authentication portal. A customer application must
use authenticated support tokens and reviewed database grants/RLS.

### Customer-facing support console starter

Generate a standalone support workspace with:

~~~sh
gregale init --template data-api-support-console --path support-console
cd support-console
npm ci
npm test
npm run demo
~~~

The synthetic demo includes organization sign-in through OIDC code/PKCE, two
tenant-scoped staff identities, 120 cases, related replies, search, filters,
paging and bounded CSV export. It is separate from the development inspector and
uses no staging credentials. The production entrypoint does not use the demo.

Configure a real OIDC application and the same JWT issuer/JWKS used by your support
Data API. Staff access tokens must have the correct API audience, a tenant claim,
and the trusted boolean read-only claim. The console keeps tokens out of browser
JavaScript in authenticated encrypted HttpOnly cookies, verifies tokens before
each read, and forwards only approved GET requests with a pinned schema contract.
The server allowlists view columns, search/filter/sort choices and complete
relationship mappings, and strips unapproved upstream properties. Database
column grants and RLS remain the authorization boundary for direct API clients.

The starter includes a configurable approved-view file, an authorization test
suite, Dockerfile, Gregale hosting configuration and deployment instructions.
Follow its README to register the callback, configure scopes/audience and client
authentication, set private session keys, connect reviewed views and pin the
schema fingerprint before deploying. Sessions expire at token expiry or 30
minutes without refresh tokens. Sign-out clears the browser cookie; immediate
provider/session revocation requires provider-specific support. Test real staff
and tenant authorization against your issuer and database before exposing customer
records. No deployment or provider account provisioning happens automatically.

## Local load and recovery verification

Run `make data-api-load-check` for portable verifier contracts and
`make data-api-load-verify` for disposable PostgreSQL REST/RPC traffic, pressure,
write reconciliation and recovery checks. The [load verifier guide](../tests/data-api/load/README.md)
describes explicit configuration, workload bounds, redacted evidence and cleanup.
The acceptance workflow includes these checks. The [manual staging canary](../tests/data-api/staging/README.md#hosted-load-and-recovery)
also includes a hosted adapter for public-edge traffic, receipt reconciliation,
managed restart and observed park/wake, with explicit unsupported coverage.
Hosted capacity and native lifecycle qualification require live evidence.
