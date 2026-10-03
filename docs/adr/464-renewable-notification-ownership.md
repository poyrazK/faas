# ADR-464: Renewable notification ownership

Status: accepted

Date: 2026-10-03

## Context

Durable replay leases a notification for thirty seconds, but image conversion,
scanning and hosting verification can take longer. Replay can reclaim a healthy
worker's row while it is still running. The LISTEN subscriber previously ran
without any claim, so replay could overlap after the five-second wakeup grace.
Deployment locks prevent duplicate preparation, but waiting deliveries still
spend attempts and occupy workers. Untokened acknowledgement could also close
another worker's active claim.

## Decision

Keep the existing outbox and delivery budget. Replay and imaged's durable LISTEN
path claim a row before handling it and renew the lease every third of its
duration. PostgreSQL's clock determines expiry and renewal. A renewal query is
bounded to one renewal interval. Failure to prove continued ownership cancels
the handler and leaves the row processing for recovery after lease expiry.
Shutdown cancellation does the same; it does not terminalize accepted work.

Immediate delivery claims the supplied ID and channel, using the stored payload
and its stored node owner. Only a first attempt bypasses the wakeup grace.
Repeated broadcasts respect retry backoff. Foreign-node, busy, settled and
dead-letter rows do not run the handler or spend attempts. Unowned handler
outcomes retain ADR-462's fenced release without spending a retry attempt.

Renewal, completion, failure and unowned release require a matching token and
an unexpired lease. Expired tokens cannot resurrect a lease before takeover.
Each mutation materializes the locked row before evaluating expiry, so time
spent waiting for a row lock cannot revive or settle an expired claim.
The shared runner counts delivery only when its completion updates the row.
The low-level CompleteNotification and FailNotification APIs retain stale-token
no-op compatibility. Legacy scheduler LISTEN acknowledgement can close only
pending rows; it cannot preempt an active replay claim. Scheduler LISTEN dispatch
remains legacy in this slice, including the existing prime recovery safety net.
Snapshot-prime replay also retains the daemon context when dispatching to the
scheduler work pool, so completion of its delivery does not cancel queued work.
Synchronous replay handlers use the claim context and observe ownership loss.

## Consequences

Healthy long operations keep their delivery ownership without spending retry
attempts. Process death still permits another worker to take over after expiry.
Delivery remains at least once: a crash after external effects and before
completion can replay them. Handler idempotency, deployment fencing and durable
checkpoints remain necessary. Cancellation is cooperative; a handler that ignores
its context can keep running, but cannot complete or requeue a replacement claim.
No VM lifecycle ownership or public API contract changes.

No additional schema migration is needed. Apply ADR-462's ownership migration
before these binaries, and upgrade all imaged workers before relying on fleet-wide
delivery serialization. Older binaries can still run without claiming first.

## Verification

PostgreSQL tests hold handlers across multiple lease periods, race immediate
delivery with replay and other subscribers, replace claims during work, block
renewal, and recover abandoned claims. They also verify stored payload and node
identity, backoff, expiry without takeover, stale acknowledgement and settlement,
shutdown, retry exhaustion, and skipped-owner accounting. Imaged integration
tests exercise the actual immediate dispatcher alongside durable replay and
preserve legacy delivery without an outbox row.
