# ADR: Queue review rounds 5–8 — held retention, replay resets, wake-ups and consumers

Date: 2026-11-13
Status: Accepted
Amends: [ADR-20261112200000](20261112200000-queue-review-fixes.md), [ADR-1140](1140-worker-queue-pull-leases.md) (pull leases), [ADR-20261022105723](20261022105723-worker-queue-limits-cpu-pricing-sizes.md) (retention), [ADR-20261101093812](20261101093812-queue-replay-meets-queue-limits.md) (replay), [ADR-972](972-parallel-queue-push-dispatch.md) (push claims)
Related: [ADR-1162](1162-billing-delivery-finality-spend-cap-and-plan-history.md), [ADR-20261019104819](20261019104819-invocation-default-retention.md)

## Context

Four more read-only review rounds of the queue on 2026-11-13, after the
fixes of ADR-20261112200000 shipped, found 23 defects (R5-1 … R8-3). Four
were in those fixes. The worst made idle consumers spin lease transactions,
let held messages expire although holds were meant to keep them, and let the
reaper delete a replayed message the moment it settled again.

## Decision

**Lease long-polls and wake-ups**

1. A lease over the waiter cap that finds nothing holds the request for its
   wait without touching the database (R5-1). Answering at once made the SDK
   consumers, which poll again on an empty answer, spin lease transactions.
   The Go consumer also paces empty answers that come back early.
2. `invocation_due` fires when a row becomes pending and is already due
   (R7-1). A pending row re-deferred by a hold, or a retry whose back-off made
   it due later, notified every lease waiter and receive of the app. Rows due
   later are found by the drain's and trigger loop's one-second tick and the
   waiters' recheck. The payload names the queue, and a lease waiter wakes
   only for its own queue (R5-10). Migration 20261113180000000.

**Retention while held**

3. A suspended or abuse-held owner's pending messages restart their wait at
   half the retention instead of dead-lettering (R5-3). Skipping them only
   deferred the expiry to the first sweep after reactivation; now each has at
   least half the retention left when the hold lifts.
4. A push queue skipped for its app's spend cap, a compute budget hold or a
   capability switched off restarts the wait of messages that have waited
   half the shortest plan retention, at most once a minute per trigger
   (`RefreshHeldQueueMessages`, R5-5). `due_at` moves to now, so delivery is
   not delayed when the hold lifts. The drain's holds already did this.
5. An expired message's open push receipt moves to `dead_letter` with it
   (R6-3); left pending it was never reaped, and replay re-arms it from
   `dead_letter`.

**Replay**

6. Every dead-letter replay path clears the row's `result_retention_until`,
   `outcome_code`, `work_decision`, `quota_reserved` and `result` (R6-1,
   R8-2). The kept horizon let the reaper delete a replayed message as soon
   as it settled again, and the kept code reported `queue_message_expired`
   on a later success.
7. A message replayed while its binding is retired stays pending under that
   binding and drains once the binding is revived (R5-6 resolved by item 9;
   the dead-letter replay keeps its identity by design). A replay refused as
   `ErrQueueBindingRetired` now answers 409 `queue_binding_retired` instead
   of an internal error, and bulk replays skip it.
8. A bulk replay replays each event under its own savepoint (R8-3), so one
   event refused by a failed statement no longer aborts the rest.

**Bindings**

9. Re-creating a deleted binding revives it when its name matches; the new
   queue name applies as a rename (R5-2). Leases and push claims select the
   binding's messages by identity, so its held messages drain under the new
   name. Taking a retired binding's queue name under another name is still
   refused.
10. Stage pull bindings: GitOps reports an enabled one as unsupported, and an
    environment clone copies production pull bindings disabled (R5-7). The
    API already refused them.
11. `queue setup` keeps an existing binding's enabled state (R5-9);
    `queue bindings update --enabled true` turns it back on.

**Delivery**

12. A queue batch whose gateway answer does not parse retries like a failed
    post instead of dead-lettering every message as a poison record (R7-2).
13. Push claims last the gateway's batch bound plus a margin (5.5 min), not a
    flat 10 min floor (R5-11), so a schedd that dies mid-dispatch stalls a
    binding at its concurrency for less time. Long-timeout apps keep their
    longer claims.
14. A pull lease ends when its worker instance stops or fails (R6-4): its
    expiry moves to now, and the lease reaper redelivers or dead-letters it.
    The attempt stays spent, since the worker may have died of the message.
    Parked and frozen instances keep theirs: a restore resumes the handler.
    Migration 20261113180100000.
15. A worker token may address its app by a slug the app had before a
    rename, unless another app holds that slug now (R8-1). Pull consumers
    configured with the old slug kept failing until redeployed.

**Reads and SDKs**

16. Per-name queue state also counts the messages of the binding that reads
    the name now (R7-3), "oldest pending" is the oldest message ready for
    delivery, and the dead-letter count is bounded by `QueueStatusCountLimit`.
17. Legacy `queues/receive` responses carry `state` (R6-5): a failed or
    cancelled message looked processed.
18. The Node and Python consumers extend every third of the visibility
    timeout, tell the handler its lease is lost once no extend has held for a
    whole timeout, and retry a failed ack or nack (R6-2).
19. The Go SDK gains `Client.ConsumeQueue` (R5-12).

## Not changed

- **R5-4** Releasing a lease whose caller disconnected before the response
  keeps refunding its attempt. The consumer never received the message, so
  nothing it does can be caused by the message, and no poison loop exists.
- **R5-8** A pull message keeps the retry policy stamped at send; a push
  delivery reads its binding's current policy and ignores a per-send
  override. Documented. Making push honor per-send policies needs the
  trigger retry state to read the message's policy.
- **R5-6** Replay onto a retired binding is not refused: it is the recovery
  path once the binding is revived (item 9). A retired binding's held
  messages still expire after the plan's retention; holding them forever
  would keep them in the app's depth and backlog. Documented.
- **R6-5 broadcast** Legacy receive still shows every settled message to
  every waiting caller and has no cursor. Documented; pull leases replace it.

## Consequences

Two migrations, both expand-only. Release N-1's lease waiters ignore the new
`queue_name` notify key and wake as before. A held account's queue messages
take one write each per half retention.
