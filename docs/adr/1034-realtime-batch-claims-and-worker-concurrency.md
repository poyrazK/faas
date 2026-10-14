# ADR 1034: Batch claims and concurrent consumer-group workers

Status: accepted for preview implementation; runtime validation pending.

## Decision

Add account and scoped-worker `POST .../claims/batch` endpoints accepting a
required limit of 1–16. Account callers supply a worker name; scoped workers use
their credential identity and existing `claim` permission. Reuse the group lock,
transactional credential authorization, expiry handling, snapshot admission and
independent lease tokens. Cap the batch by the remaining group-wide 32 active
lease slots. All claimed rows, assignment cursor advances and any expired-lease
dead-letter webhooks commit together; failed transactions return no usable batch.
No database schema change is needed.

Return 200 with `claims`, `history_unavailable` and `capacity_blocked`, including
empty and partial batches. Reuse eligible ready snapshots before assigning new
history. Advance the local assignment cursor after each inserted snapshot so a
batch never assigns the same sequence twice. A retention gap or the 128-snapshot
cap stops further assignment without discarding claims already obtained in the
transaction. Empty batches can also mean caught up, delayed retries or exhausted
lease slots. Unexpected database errors roll back the entire batch.

Retain single-claim endpoints and their existing 204/409/429 behavior through a
one-item batch wrapper. Each batch item is renewed, completed or retried through
the existing settlement API; no batch-wide acknowledgement or lease ownership.
Tokens remain hashed at rest and bound to the same worker credential. Claim
responses are not cached and uncertain claims must not be retried automatically.

Add Node helper options `concurrency` (1–32, default 1) and `batchSize` (1–16,
default min(concurrency,16)). Never claim more than available in-flight handler
slots; there is no local prefetch queue. Default workers keep using single claims.
Each handler renews and settles independently. Finish returned claims before
reporting a partial-batch blocker. Fatal network/auth/lease or error-callback
failures cancel other handlers and stop new claims. Shutdown aborts handlers and
renewals, waits for handlers to exit, and leaves unsettled claims to expire.
Handlers must honor cancellation and use idempotent effects; cancellation cannot
undo effects and a handler that ignores its signal can delay shutdown.

Concurrent handlers and group replicas can complete out of sequence. Batching
reduces claim requests but does not reduce per-message settlement/renewal requests
or establish exactly-once processing. SDK concurrency is a local bound, not a
fleet-wide worker limit or a promise to preserve per-entity ordering.

## Validation

Go and Node builds, generated Node/Python specifications and static checks are
the validation scope. Runtime and database checks remain deferred by request.
Existing consumer-group and worker-credential migrations are still prerequisites.
