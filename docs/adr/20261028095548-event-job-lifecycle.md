# ADR: Event backfills end on their own, and failing recovery jobs back off

Date: 2026-10-28
Status: Accepted
Amends: [ADR-639](639-durable-subscription-event-backfill.md) (backfills run until every item settles), [ADR-797](797-durable-bulk-event-recovery.md) (a recovery job's due order), [ADR-913](913-recovery-receipt-retention-holds.md) (a running backfill holds its whole range)
Related: [ADR-794](794-durable-workflow-event-backfill.md), [ADR 20261023215834](20261023215834-event-plan-caps.md)

## Context

GA gaps from the 2026-10-25 event audit:

- **A backfill could not end early.** Its states were `running`,
  `completed` and `completed_with_failures`, and there was no cancel route.
  A backfill whose target subscription was paused, or whose consumer stayed
  circuit-open, kept up to 100 deliveries waiting and stayed `running`
  forever, holding one of the account's three backfill slots and the
  subscription's one.
- **A running backfill held its whole range.** Pruning skipped every
  settled receipt the account accepted in the job's `[from, cutoff)`, the
  ones the scan had already passed included, so a stuck backfill could
  double what an account kept against its storage budget and lead to 429s
  on publish.
- **One failing job stalled everyone.** Recovery jobs are taken in
  `next_attempt_at` order and backfill scans oldest first. An error rolled
  the work back without moving the job, and the sweep stopped for that pass,
  so the same job came first on every pass: recovery stopped for every
  account until that job expired (24 h), and backfill scanning stopped with
  no limit.

## Decision

1. **Cancel.** `POST /v1/event-replays/{jobID}/cancel` (deploy write scope,
   the same company access as reading the job) ends a running backfill as
   `cancelled`. `gregale event backfill cancel JOB_UUID --yes` calls it.
   Cancelling an ended job returns it unchanged.
2. **Idle expiry.** A running backfill that makes no progress for
   `EventReplayBackfillIdleExpiry` (24 h) ends as `expired`. Progress is
   anything that moves its `updated_at`: a scanned page, a settled item or a
   retry. A slow but moving backfill over millions of receipts keeps
   running; one stuck behind a paused or circuit-open consumer ends a day
   later.
3. **What ending does.** In one transaction the job becomes `cancelled`,
   `expired` or `failed` and `completed_at` is set. A second step, repeated
   by schedd until it finds nothing, marks the job's pending and processing
   items `cancelled` and deletes its unsettled delivery rows, then settles
   their receipts. A delivery already admitted keeps its invocation and
   finishes; nothing new is admitted, because the recipient claim and the
   admission check both require a running job. Ended states are final: a
   trigger skips any later update of an ended job, so an older release's
   finalize step cannot reopen one during a rollout. Retry is refused for
   ended jobs (409 `event_replay_backfill_state`).
4. **The hold follows the scan.** A running backfill holds only receipts
   its scan has not reached: accepted in its range and after its cursor.
   Receipts it already staged are `processing` and never pruned, and
   receipts it skipped are not its to keep. The 30-day hold on retryable
   failures of `completed_with_failures` jobs is unchanged.
5. **A failing job backs off, then fails.** When processing a recovery or
   backfill job returns an error, the work rolls back as before; then, in
   its own transaction, the job's `failure_count` goes up, its next attempt
   moves `EventJobFailureBackoffMin` (5 s) doubling up to
   `EventJobFailureBackoffMax` (1 h) later, and a short failure code is kept
   (`conflict`, `invalid_job`, `timeout` or `internal`; the full error is
   only logged). The sweep carries on with the next job. A success resets
   the count. After `EventJobFailureLimit` (10) failures in a row, about 85
   minutes:
   - a backfill ends as `failed`, with `last_failure` in its status;
   - a recovery job is cancelled through the existing path, with a history
     entry by the system actor `recovery_failure` whose reason names the
     failure code. Recovery keeps its four states, notifications and webhook
     events; a fifth state would change all of them for a rare outcome.
   An error while the sweep is stopping, or while recording the failure,
   does not count.

## Consequences

- No backfill or recovery can block another account's for more than one
  backoff step, and none can hold receipts or a slot for more than a day
  without moving.
- New response values, all additive: backfill `state` gains `cancelled`,
  `expired` and `failed`; the item state `cancelled`; and the optional
  `last_failure`. Older clients show the new strings as-is.
- Release compatibility: the migration only adds columns, widens checks and
  narrows the hold function. An older schedd ignores the backoff (it may
  retry a failing job sooner than this release would) and cannot reopen an
  ended job; an older apid lists ended jobs with their new state strings.
