# ADR-1734: Data API deadlines across SQL transaction retries

Status: Accepted

## Context

Pinned PostgREST 14.3 runs Hasql retrying transactions. SQLSTATE `40001` and
`40P01` restart the transaction, including its pre-request hook. A statement
timeout can restart on each attempt. The gateway's monotonic 15-second HTTP
deadline does not establish that a disconnected SQL request stopped retrying.

## Decision

Add opt-in `DATA_API_SQL_DEADLINE=1`; absence preserves the existing engine hook
selection. Reject other values. Reuse the canonical 15-second query and 5-second
lock limits. Capture one absolute epoch-millisecond expiry when HTTP admission
starts the existing deadline. Strip the reserved `gregale_sql_deadline` claim
from every external JWT. When enabled, sign an internal version-one object with
that expiry and the runtime's coordinated-refresh mode. Verification, refresh,
signing and body/pool delays consume the same budget. Re-signing or retrying at
the SDK is not introduced. All attempts inside one engine request reuse the JWT.

Select `gregale_data_api.check_deadline_request` as the engine pre-request hook.
It invokes `enforce_deadline`, optionally invokes the existing refresh barrier,
then recalculates the remaining budget after the barrier wait. Both new functions
are `SECURITY INVOKER`, volatile, with a fixed `pg_catalog` search path and
protected ownership. They read signed claims and the database clock, change only
transaction-local timeout settings and require no new table or pool. Each lock
and subsequent statement receives at most the remaining time and existing
canonical cap. Existing stricter nonzero timeout settings are preserved. The
private control schema stays outside exposed schemas. Refresh gate semantics
and its one-connection engine/one-connection reader reservation are unchanged.
Opt-in also sets `PGRST_DB_CONFIG=false`, because in-database engine settings
otherwise override environment settings and could replace the selected hook.
This disables PostgREST's in-database engine/role configuration loading in this
mode. PostgreSQL login defaults and trusted function settings remain independent;
disabled runtimes keep their previous configuration-loading behavior.

Validate the exact object keys, types, version and integer expiry. An expired
deadline raises a nonretryable `PGRST` error with safe fixed JSON code
`data_api_sql_deadline_exceeded` and HTTP 504. A missing, malformed, unsupported
or more-than-15-seconds-future contract fails closed with
`data_api_sql_deadline_invalid`, HTTP 500. Error details include the pinned
engine's required empty headers object. No user values, claims, SQL or database
identifiers enter these error bodies. PostgreSQL can instead interrupt a main
statement with SQLSTATE `57014`, or a lock wait with `55P03`; these codes do not
establish which timeout fired. The HTTP deadline can win the response race.

Installation is explicit through `sql-deadline.mjs install BINDING_ROLE`, using
the owner-authorized migration connection. Installation is transactional and
checks the serving role's catalog permissions after grants, without requiring
the installer to assume that login. An elevated login, membership, mutable
control schema or binding-owned function is refused. The separate `check` command
uses a read-only transaction and never installs or repairs objects. Check the
exact version-one function bodies, signature, volatility, security attributes,
search path, protected ownership, schema permissions and execution grants.
Refresh composition also requires the existing protected refresh hooks.

Before spawning an engine, opt-in startup checks PostgREST 14.3 and verifies the
installed contract through a single temporary serving connection. That connection
closes before engine/refresh pools open. Startup never adopts or modifies a hook.
There are no additional customer-request probes. An absent or mismatched install
prevents startup. Existing generated images need rebuilding, and every new or
rotated serving login needs its own authorized hook grants before adoption.
Credential rotation does not automatically install these grants. Other bindings
and disabled runtimes retain their previous hook selection.

[ADR-1735](1735-data-api-deadline-grant-rotation.md) subsequently adds explicit
owner-approved deadline grants across rotation for managed Neon bindings in
databases without coordinated SQL refresh gates. Manual setup remains the default.

The SDK classifies `data_api_sql_deadline_exceeded` as `timeout` / `reconcile`,
including throwing RPCs. Invalid contracts remain unknown server configuration
errors. Existing statement cancellation and lock-conflict classifications remain
unchanged. No HTTP mutation replay, SQL transaction retry policy change or new
automatic recovery is added. The starter's existing timeout path retains the
same receipt key for explicit recovery.

## Limits

Epoch deadlines require stable, reasonably synchronized gateway/database clocks.
An ahead database clock shortens the budget; a substantially behind database
clock fails the future-horizon check. Backward clock steps can weaken elapsed-time
bounds. The HTTP deadline remains monotonic and independent. A request already
expired in a pool is rejected when its hook eventually executes; this cannot
interrupt pool acquisition or SQL ahead of that hook. Statement/lock timeout
timer scheduling is not a hard real-time guarantee.

Trusted owner-authored SQL can override settings, catch cancellation, use
function-specific GUCs or produce nontransactional/external effects. This is a
default execution contract for normal Data API requests, not a sandbox boundary
or universal proof of SQL cancellation. Failed attempts roll back transactional
writes; sequences, external effects and earlier successful requests are excluded.
Owner changes after startup still require operational coordination and checks.
Provider/native/hosted rollout qualification and aggregate budgets stay separate.

## Validation

Unit checks cover strict opt-in, hook composition and unchanged pools, forged
claims/headers, disabled stripping, admission-time expiry and owner command
validation. Native PostgreSQL/PostgREST checks cover absent/mutated hooks, read-only
verification, protected grants, malformed/future/expired contracts and preservation
of stricter timeouts. A short signed deadline interrupts subsequent main SQL.
Repeated injected `40001`/`40P01` faults demonstrate actual engine transaction
retries, original-deadline termination after HTTP cancellation, rolled-back writes,
other-user progress, RLS and pool recovery. Retry-counter stability is required;
one idle session snapshot cannot establish completion between attempts. Existing
real deadlock and legacy coordinated-refresh tests remain regression coverage.
Composed-hook checks verify barrier wait consumes the budget and pause/resume
continues to work. SDK checks preserve uncertain-outcome guidance without replay.
