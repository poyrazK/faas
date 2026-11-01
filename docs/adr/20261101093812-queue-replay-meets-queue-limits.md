# ADR: A replayed queue message meets the queue limits and gets a fresh wait

Date: 2026-11-01
Status: Accepted
Amends: [ADR-20261022105723](20261022105723-worker-queue-limits-cpu-pricing-sizes.md) (message retention and the queue caps)
Related: [ADR-1140](1140-worker-queue-pull-leases.md), [ADR-20261031101000](20261031101000-keyed-expiry-spares-started-work.md), [ADR-134](134-durable-async-contract.md)

## Context

The 2026-11-01 blocker hunt (event-driven GA, hunt #6) found five queue
replay and lease defects:

1. **Replaying an expired message expired it again.** Retention counted only
   from `created_at`, and no replay path reset it. A message dead-lettered as
   `queue_message_expired`, and replayed as the docs say to, was expired again
   by the next 60 s sweep. So was any older message replayed from the dead
   letter after its plan's retention.
2. **Replay ignored the queue caps.** The queue DLQ retry, the unified DLQ
   replay (single and replay-all) and the queue receipt replay moved rows back
   to `pending` without the per-app depth and per-account backlog-bytes checks
   a send meets.
3. **Account replay-all could return 0 forever.** It took the first `limit`
   open entries and skipped the expired keyed ones among them. A first page of
   expired keyed work replayed nothing on every call and never reached the
   entries behind it. The store's own replay-all already leaves them out.
4. **A lease that lapsed on the last attempt went back to pending.** The
   message waited for a later lease call to dead-letter it. With no consumer
   it expired after the plan's retention, under the misleading
   `queue_message_expired`.
5. **Cancelling an app's work returned reserved async slots to the account,**
   not to the subject that reserved them (an org that owns the app's
   resources).

## Decision

1. **Retention counts from the last replay.** A pending queue message expires
   once `coalesce(last_replayed_at, created_at)` is older than its plan's
   retention. Every replay path already stamps `last_replayed_at`. The sweep
   keeps its `created_at` bound, which still narrows
   `invocations_queue_pending_created_idx` because a replay is never earlier
   than the send.
2. **A replay meets the send's caps.** Replaying a production queue message
   checks its app's pending and dispatching production queue rows against
   `MaxQueueDepth` and its account's pending queue bytes against
   `MaxQueueBacklogBytesPerAccount`, with the same checks and errors as a
   send. A single replay is refused with `403 plan_queue_depth` or
   `403 plan_queue_backlog_bytes` and the message stays in the dead letter.
   Replay-all skips such messages. When it replays nothing else, it answers
   with the first refusal instead of `replayed: 0`. Environment-owned
   messages keep their own caps. The check counts without the app lock a
   send takes: a replay racing a send to the same app may pass the depth cap
   by the messages in flight, the same allowance the bytes cap already gives
   concurrent sends to different apps.
3. **Account replay-all pages past what it cannot replay.** It walks the
   visible ledger until it has replayed `limit` entries. It skips expired
   keyed work (checked from the source row, without a replay transaction), rows
   changed meanwhile and queue-capped messages, and it stops after `limit`
   queue-cap refusals.
4. **A lapsed last-attempt lease dead-letters at once.** When the lease
   reaper finds a pull lease whose message has no attempt left under its
   retry policy and plan, it moves the message to the dead letter with outcome
   code `queue_lease_attempts_exhausted`. The lease call's own exhaustion
   path uses the same code. Other lapsed leases still return to pending.
5. **A cancelled reservation goes back to its subject:**
   `coalesce(async_quota_subject_id, account_id)`, as the lease reaper does.

No migration. The response schemas are unchanged: replay-all reports skipped
messages through the limit problem when it replays nothing.

## Consequences

- "Replay it once a worker can take it" works: the message gets the plan's
  whole wait again.
- Replay can no longer grow a queue past its plan's depth or an account past
  its backlog bytes.
- A dashboard or CLI replay-all against a full queue shows the limit problem
  instead of silently replaying nothing.
- A pull consumer that stops leasing no longer leaves exhausted messages
  pending for days. They reach the dead letter when their lease lapses, under
  a code that says why.

## Rejected alternatives

- **Resetting `created_at` on replay.** It rewrites the message's identity
  timestamp, which ordering, history and retention of the finished row read.
- **A `skipped` count in the replay-all responses.** It changes the public
  schema and every generated SDK. The limit problem carries the same
  information when it matters, when nothing replayed.
- **Locking the app row in replay.** Replay already holds lane and row locks.
  Taking the app lock after them inverts the order a send takes, and two
  replays that inserted rows referencing the app could deadlock upgrading
  their key-share locks.
