# ADR-20261022203653: One dead-letter entry per queue message

- **Status:** Proposed
- **Date:** 2026-10-22
- **Amends:** [ADR-179](179-unified-dead-letter-ledger.md) (the unified ledger lists every captured failure). A queue or delayed-task message's push receipt is now captured but not listed.
- **Related:** [ADR-607](607-unified-event-receipts.md) (receipt recovery actions address the invocation entry), [ADR-610](610-keyed-dead-letter-replay-claim-exclusion.md) (keyed replay lock order)
- **Finding:** Hunt 12 H12-58 (production-us, app `h12-q`, push queue binding)

## Context

A push-delivered queue message has two rows: its invocation, and a trigger
record of the binding's `kind='queue'` trigger whose `item_identifier` is the
invocation ID (the push receipt). When the handler fails for good, both reach
`dead_letter`, and ADR-179's two capture triggers write two
`dead_letter_events` rows, one per source.

On production-us, `gregale dlq list` showed one failed message as an
`invocation` entry (3 retries, `max_attempts`) and a `trigger_record` entry
(2 retries, `function state=failed`). `gregale queue status` counted one dead
letter. `gregale dlq replay --all` reported `replayed 1`: replaying either
entry restarts both rows (the receipt replay resets the invocation; the
`queue_replay_receipt_rearm` trigger re-arms the receipt when the invocation
leaves `dead_letter`), so the other entry's replay finds nothing to reset,
returns not-found and stays open. When the message failed again, both rows
were captured again: two entries.

The two entries are one failure. The invocation holds the authoritative
attempt count, error, replay generation and queue binding, and ADR-607
receipts and recovery actions already address the invocation entry. The
receipt duplicates it.

## Decision

The unified dead-letter list shows one entry per queue or delayed-task
message: its invocation. The push receipt's failure stays recorded and is
recovered through the invocation.

- **What is hidden.** `production_dead_letter_events` excludes rows with
  `source = 'trigger_record' AND origin = 'queue'`. Every unified read, replay
  and purge goes through this view: app and account lists, inspect, single and
  bulk replay, single and bulk purge. The rows are still captured into
  `dead_letter_events`, and the receipt's failure and its `failure_history`
  stay in `trigger_dead_letter` (`gregale triggers dlq`).
- **Why `origin`.** `origin` is the trigger kind, captured when the receipt
  failed. A `kind='queue'` trigger is dispatched only by the queue poller, which
  accepts only the `queue` and `delayed_task` sources (`newQueuePoller`), so
  every dead-lettered `kind='queue'` record is a receipt that names an
  invocation. Joining `triggers` at read time
  (`t.kind='queue' AND t.source IN ('queue','delayed_task')`, as the replay
  queries do) would stop matching once the binding's trigger is deleted:
  `dead_letter_events.trigger_id` is `ON DELETE SET NULL`, and the orphaned
  receipt would reappear beside its invocation.
- **Inspect.** A receipt's unified event ID now returns 404 on inspect,
  replay and delete, like a purged event.
- **Replay.** Replaying the invocation entry resets the invocation; the
  existing `queue_replay_receipt_rearm` trigger re-arms the receipt in the same
  transaction with a new claim generation. The receipt keeps its UUID, payload,
  work lane and failure history (ADR-610). The queue-specific
  (`RetryQueueDeadLetter`) and trigger-specific (`RetryTriggerRecordByOperator`)
  replay routes are unchanged.
- **Replay-all counts.** Candidates come from the view, so `replayed N` counts
  messages. Replaying a message no longer leaves its other entry open, and a
  second bulk replay finds nothing.
- **Purge.** Deleting the invocation entry, singly or in bulk, removes the
  message from the list. The receipt row is hidden whether or not the
  invocation entry exists, so a purge cannot resurface it. Bulk purge counts
  messages. Hidden receipt rows leave `dead_letter_events` with the existing
  retention purge (`PurgeExpiredDeadLetterEvents` reads the base table).
- **Keyed replay lanes.** The lane comes from the invocation:
  `LockDeadLetterReplayLanes` and `LockKeyedDeadLetterInvocationRows` already
  derive it from an invocation entry. The re-armed receipt row is locked by the
  rearm trigger after the ledger row, as single-entry invocation replay and
  `RetryQueueDeadLetter` already do. While its invocation is dead-lettered the
  receipt cannot be claimed or failed, so nothing else holds it. The
  receipt-to-invocation branches of `LockDeadLetterReplayLanes`,
  `LockKeyedDeadLetterInvocationRows` and `LockKeyedDeadLetterTriggerRows` are
  no longer reached for queue receipts through the unified API. They stay until
  a later cleanup, and still cover external broker receipts.
- **ADR-607 receipts and recovery.** Event-receipt and execution-recovery
  queries join the view on `source = 'invocation'` only and are unchanged.
- **Scheduler order.** A receipt is hidden only because its invocation is
  listed, so a receipt must never dead-letter while its invocation does not.
  For queue pollers, schedd now settles a per-record dead-letter result the
  way its batch failure paths (post error, malformed response, rate limit)
  already did: it Nacks the invocations first, and `QueueFinishDeliveryClaims`
  moves each invocation and its receipt to `dead_letter` in one statement.
  Only then does it write the receipt's `trigger_dead_letter` audit row, and
  only for the reason groups whose Nack succeeded. A failed Nack leaves the
  invocation and the receipt claimed; the lease reaper returns them for
  redelivery. Broker triggers keep writing the receipt first, because for them
  the receipt is the durable record.
- **MemStore.** `deadLetterEventsLocked` applies the same rule to live trigger
  dead letters and to retained snapshots.

## Release compatibility

- One expand-only migration (`20261022203653422`) starts with
  `SET LOCAL lock_timeout = '5s';` and runs `CREATE OR REPLACE VIEW` with the
  same columns in the same order; only the `WHERE` clause changes. The view
  lock waits behind readers for at most 5 s. No table, column, trigger or
  function changes, and no rows are rewritten.
- apid N-1 beside N: both read the view by name, and sqlc expanded every
  `d.*` into explicit columns that are unchanged. The previous release lists one
  entry per message as soon as the data lane applies the migration, before any
  daemon restarts. No proto, OpenAPI or guest JSON changes.
- Clients of either release: an event ID cached for a receipt entry returns
  404, which they already handle for purged events.
- Down migration restores the previous `WHERE` clause; hidden rows reappear
  unchanged.

## Consequences

- For push queues, `gregale dlq list` and `gregale queue status` count the same
  failed messages.
- Receipt reasons (for example `function state=failed`, `poison_record`) no
  longer appear in the unified list. They remain in `gregale triggers dlq`.
- A receipt can no longer dead-letter while its invocation does not (see
  Scheduler order). If schedd stops between the Nack and the audit write, the
  message is listed through its invocation and only the receipt's
  `trigger_dead_letter` audit row is missing.
- Queue receipt rows of purged invocation entries stay in `dead_letter_events`
  until the existing retention purge removes them. They are audit evidence,
  invisible in the unified list, and a purge does not delete them early.

## Rejected alternatives

- **Stop capturing queue receipts into `dead_letter_events`.** It changes a
  capture trigger both releases run, cannot be undone for rows already skipped,
  and drops the projection's copy of the receipt failure. The view change is
  reversible.
- **List the receipt and hide the invocation.** The invocation carries the
  attempt count, error, replay generation and binding, and ADR-607 recovery
  actions target it.
- **De-duplicate in the API handler.** Cursor pages, bulk replay and bulk purge
  limits are computed in SQL; filtering after the query would return short
  pages and miscount bulk actions.
- **Join `triggers` in the view.** The trigger ID is cleared when the trigger
  is deleted, which resurfaces orphaned receipts.
- **Store a marker column, like `environment_owned`.** It needs a column, a
  capture-trigger change, a backfill and new sqlc fields. `origin` already
  records the fact.

## Open questions for the owner

1. Should the receipt-to-invocation branches in the keyed replay lock queries
   be removed for queue receipts after one release (a contract cleanup), or
   kept for defence in depth?
2. Should `gregale dlq inspect` show the receipt's reason next to the
   invocation entry (one read-only join), so the `function state=failed` detail
   is not only in `gregale triggers dlq`?

## Qualification

`queue_dead_letter_message_is_one_entry` in the shared state conformance suite
runs the same cases on MemStore and PostgreSQL. For a failed push-delivered
message it checks: listed once in app and account lists; single replay re-arms
the receipt; bulk replay counts 1 and then 0; a repeat failure is still one
entry; single and bulk purge leave no entry and count 1. Every case also checks
that the receipt stays in the trigger dead-letter audit. Without this change
all six cases fail on both stores, because the failed message is listed as two
entries (an `invocation` and a `trigger_record`). The `receipt_event` surface of
`queue_dead_letter_replay_rearms_original_receipt` was removed because the
receipt entry no longer exists; its queue, invocation-entry and receipt replay
surfaces are unchanged.

`TestSettleTriggerDeadLettersOrdersQueueNackFirst` (pkg/sched) pins the
scheduler order: a queue poller Nacks before writing receipts and writes none
for a reason group whose Nack fails, while a broker still writes its receipts
before the Nack.
