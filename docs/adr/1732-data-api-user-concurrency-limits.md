# ADR-1732: Data API per-user concurrent request limits

Status: Accepted
- **Amended (ADR 20261021180534):** the admission capacity follows the
  configurable engine pool, and a setting above it acts as the capacity
  instead of failing startup.

## Context

Global HTTP admission bounds active handlers and the queue before JWT verification.
Per-user token buckets bound request rates, but a user can still occupy both
serving slots with long calls. Customer requests, SQL sessions and work surviving
HTTP cancellation have different lifetimes. A process-local policy cannot qualify
or enforce a database-wide connection budget.

## Decision

Add optional `DATA_API_USER_CONCURRENCY=N` to the generated Data API runtime.
Absent means disabled; require an integer from one through the effective global
admission capacity: two normally, one with coordinated refresh. Canonical pool,
tracking and cooldown bounds live in `pkg/api/limits.go` and the runtime mirror
tripwire covers them. The setting applies to all authenticated REST, RPC, schema
and OpenAPI requests. Public health and OPTIONS retain their existing behavior.

Keep global admission and JWT verification in their existing order. After verified
subject/authorization/contract checks and rate admission, acquire a per-user slot
before any refresh, readiness or engine SQL dispatch. A slot is keyed by a digest
of the verified subject, so rotated tokens share it. Untrusted headers and claims
cannot override the policy. Each server has its own limiter. There is no extra
per-user queue, fair-queue guarantee, authentication capacity increase, tenant SQL
pool, database-wide admission or distributed quota.

Release a slot only after both the asynchronous handler and downstream response
have settled. Track potential SQL dispatch separately. A completed refresh reader,
definite admin readiness reply or engine response headers acknowledge completion
of that request's engine/control operation. Engine headers alone do not release
a still-streaming downstream response. Requests that never dispatched SQL can
release after their handler and downstream finish, including authorization and
signing failures. Pending handler work remains counted after a client disconnect.

If dispatch has no acknowledged completion when both handler and downstream
settle, retain the slot in cooldown for a full existing 15-second query budget
from that point. Timeout does not immediately free capacity for that same user.
A late genuine acknowledgement can release its own cooldown. Expiry and late
callbacks cannot release replacement slots or renew a cooldown. Cleanup uses a
bounded lazy sweep and per-user expiry check, without timers or SQL probes.
Never evict active handlers. At most 4,096 subject digests are tracked; a new user
at that bound is refused until entries can be safely removed.

The cooldown is conservative HTTP admission protection, not proof that SQL ended.
SQL may outlive HTTP abort or the cooldown; server statement timeouts, pool limits
and database/provider qualification remain independent. Restart clears the local
limiter, and other runtimes have independent limits. Existing global FIFO admission
can still reject or delay another user before their JWT is verified.

Rejections return `429 data_api_concurrency_limited`, `Retry-After`, gateway
request ID, policy/observed counts and the existing docs URL. Fixed reasons are
`subject` and `tracking_capacity`. Counts distinguish active/pending HTTP work
from cooling slots; they are not SQL-session counts. Retry-After is advisory:
one second for active work/tracking pressure, up to 15 for a cooling user. There
is no automatic mutation replay. SDK classification adds `concurrency_limited`
with recovery `wait`; the starter handles it with the existing explicit outcome
review flow.

Request logs add bounded `concurrency_limit` metadata only on rejections, with
reason, maximum, active/cooling counts and retry seconds. Raw subjects, hashes,
tokens, SQL and request values stay private. CLI diagnostics validate the safe
vocabulary/accounting, preserve metadata for request lookup and count these
separately from rates, overload and database unavailability. Health shows retained
rejections as recent findings without changing current SQL readiness. Legacy logs
remain readable. Runtime rebuild is required for an existing deployment.

## Consequences and validation

An owner can restrict a user to one long request while another serving slot
remains available. Opt-in preserves existing clients and deployment behavior.
Conservative cooldowns can reject requests even after SQL has completed when no
engine acknowledgement reached the gateway; that is preferable to treating HTTP
cancellation as SQL completion.

Unit checks cover strict policy/reservation bounds, live streaming, pending
handlers, cancellation, cooldown boundaries, late callbacks, tracking pressure,
Unicode/rotated identities, runtime independence and secret-free logs. Real
PostgreSQL/PostgREST fixtures verify RLS, concurrent users, refused REST/RPC writes
with a nontransactional invocation counter, real HTTP deadlines, uncertain
cancellation, bounded cooldown recovery and unchanged serving connection counts.
SDK tests check normal/throwing mutation failures without retries. CLI tests check
lookup, malformed metadata, historical health findings and legacy logs. Separate
fleet-wide fairness and provider/native qualification remain open.
