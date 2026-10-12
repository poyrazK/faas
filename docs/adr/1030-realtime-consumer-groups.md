# ADR 1030: Lease-fenced realtime consumer groups

Status: accepted for backend preview implementation; runtime and migration evidence pending.

## Context

Named durable WebSocket subscriptions fan out independently to each consumer.
Their processing ACKs do not coordinate competing workers. Backends need a shared
channel subscription that can reassign work after a crash without accepting a
stale worker's completion.

## Decision

Provide account-authenticated pull APIs on apid, under the existing history
preview and realtime plan/app/MFA/scope checks. This initial implementation requires
the durable PostgreSQL store; unsupported stores return a capacity error. Browser
subscription tickets and SSE credentials cannot access these backend APIs.

Each group belongs to an endpoint/channel and has an explicit initial assignment
cursor, immutable lease duration and retry budget. Serialize group operations by
locking the group row. Serialize group admission using the endpoint row. Claim
existing eligible retries first, then atomically copy exactly the next retained
sequence into a group-owned message snapshot and advance the assignment cursor.
Lock the channel head while calculating its retention floor and copying the row;
use the same highest-expired-sequence floor as channel replay. Never silently skip
an unassigned retention gap.

Store only a SHA-256 hash of a fresh 256-bit lease token. A current, unexpired
sequence/token pair is required for completion, renewal or retry. Validate again
in the final SQL mutation so processing latency cannot extend an expired lease.
A worker can renew to another configured lease interval. Completion deletes the
snapshot and increments a completion count; it does not advance an individual
principal's durable checkpoint or generate a channel processing webhook. These
are shared-group completions, separate from named per-principal ACKs.

A subsequent claim normalizes expired leases: retry them while attempts remain,
otherwise dead-letter them. Explicit retries reaching the budget also dead-letter.
Dead-letter GET includes exhausted expired leases even before that normalization.
Replay resets one dead letter's attempts and queues the original snapshot with a
fresh token on its next claim. The assignment cursor stays put, and completions
can arrive out of order. Recovery requires an expected-cursor fence, no ready or
leased work, and a forward reset to precisely the retained floor minus one after
recovering missing work from application state. Dead letters survive recovery.

## Bounds and ownership

Limits live in pkg/api/limits.go: 8 groups per endpoint, 128 total outstanding
snapshots per group, 32 live leases per group, leases of 1–300 seconds (default
30), and 1–10 attempts (default 5). An attempt means a lease was issued; renewal
does not consume another attempt. Retry delay is bounded to 300 seconds. Group
and worker names are at most 128 bytes; safe printable diagnostic codes at most
64 bytes. There is no generic response cache for claim tokens.

Snapshots intentionally outlive channel retention and remain until completion
or group deletion. Their count is bounded, and full groups stop assigning new
messages rather than discard work. The explicit group DELETE cascades snapshots
and invalidates all tokens. No automatic idle-group expiration or scheduler is
introduced; polling drives lease recovery.

Expose status and bounded dead-letter details in the existing realtime inspector.
The Node helper processes one claim at a time, renews during the handler, completes
after success and requests retry after handler failure. On shutdown it signals
the handler and leaves the lease to expire. Lost ownership or a network/auth error
terminates the worker, allowing the backend to restart it with backoff. Python and
Node generated clients expose the individual operations.

## Consequences

This is at-least-once delivery, not exactly-once execution. A crash after side
effects and before completion, or a handler continuing after lease loss, can
produce duplicates. Workers must use idempotent effects keyed by endpoint/channel/
sequence and respect their cancellation signal. Ordering across workers is not
guaranteed. Active worker counts reflect distinct names on unexpired leases,
not connected processes. An assignment cursor is not a completion checkpoint.
Unassigned lag may include events lost to retention; status explicitly flags gaps.

Apply 20261012102700470_realtime_consumer_groups.sql before deploying this apid
version. Runtime tests and migration execution remain deferred at the user's
request; build/static evidence does not validate PostgreSQL races or UI rendering.
