# ADR 1037: Bounded consumer-group long polling

Status: accepted for preview implementation; runtime validation pending.

## Decision

Accept optional `wait_seconds` (0–20, default 0) on account/scoped single and batch
claim requests. Retain current response shapes and independent claim tokens.
Return on available work, pause/drain, a retention/capacity blocker, deadline or
request cancellation. Admit at most 64 waiting requests per apid using a bounded
process-local semaphore; excess requests return 429 with the observed limit.
Immediate claims do not consume waiting slots. No schema change is required.

Implement the preview with 500 ms periodic claim attempts, releasing the group
transaction and PostgreSQL connection between checks. Each attempt uses the same
current worker authorization, group locking and expiry logic. Credential revocation,
rotation, expiry and permission checks remain effective while waiting. Paused or
draining groups return without waiting once observed. All-empty attempts can also
recheck delayed retries or full lease slots. Return a nonempty batch immediately
rather than waiting to fill it. Blockers stop waiting and retain existing single
claim error or batch partial-result semantics.

This reduces idle HTTP traffic, not database polling. The semaphore bounds HTTP
waiters per process, not across the fleet or per tenant. Sleeps stop at the wait
deadline; an in-flight database attempt remains governed by the parent request
budget and can extend actual duration beyond the configured wait. Do not hold a
transaction over the sleep and do not use one dedicated LISTEN connection per
request. Future shared notification wakeups can replace periodic checks without
changing the claim contract.

Each committed attempt counts in scoped credential usage, including empty attempts;
counts are database operations rather than incoming HTTP request counts. Expiry
handling can emit dead-letter webhooks while waiting. A cancelled or uncertain
claim may have committed a lease that was not observed by the caller; its eventual
expiry permits redelivery. Do not retry uncertain claims automatically.

Node processing helpers default to 10-second waits, with `waitSeconds: 0` retaining
immediate behavior. Existing empty-result backoff avoids rapid loops on pause.
Keep concurrency and local capacity bounds, cancellation and individual settlement.
Direct isolated clients and generated Node/Python APIs expose the optional wait.
Configure proxy/client timeouts above the wait plus request overhead. Claim routes
use the existing 30-second request-budget ceiling rather than the five-second
admin default, while retaining shorter caller deadlines.

## Validation

Go/Node builds, static request limits/security/spec parity and generated Python
syntax checks are the validation scope. Runtime/database validation remains deferred
by request. Existing consumer-group, pause/drain and credential migrations remain
prerequisites; this feature adds no migration.
