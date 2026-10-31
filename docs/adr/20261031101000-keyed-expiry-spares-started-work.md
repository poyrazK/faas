# ADR: Keyed work expires only before it starts

Date: 2026-10-31
Status: Accepted
Amends: [ADR-609](609-safe-keyed-invocation-replay.md) (keyed replay keeps its deadline)
Related: [ADR 20261031100001](20261031100001-event-delivery-failures.md) (failed event deliveries), [ADR 20261029190000](20261029190000-event-delivery-retry-backoff.md) (event delivery retry backoff)

## Context

A work policy's `expires_after_ms` stamps `work_expires_at` on keyed work.
`docs/work-policies.md` documents it as expiring "work that has not begun
before its deadline". The code expired any *pending* keyed row past its
deadline (`pkg/state/work_policy.go`: the periodic sweep and the lane check
at claim time). A row waiting out a retry is pending too. So a keyed event
delivery whose handler failed, and whose next retry fell after the
deadline, was moved to `expired`. It was not dead-lettered, not counted as a
failure, and not replayable from the dead-letter queue. The failure was lost
silently.

The same happened to delivered pull-queue items that were nacked, and to
keyed broker records waiting in `retry`.

Replaying such a row from the dead-letter queue had a second problem. Replay
resets `attempts` to 0 and keeps the deadline, as ADR-609 requires, so the
replayed row expired again on the next sweep. The API answered 202, and the
work then disappeared.

The owner chose (2026-10-31): expire only unstarted work, and refuse a
dead-letter replay that would only expire.

## Decision

1. Only keyed work with `attempts = 0` expires. This applies to invocations
   in the sweep and in the lane check, and to keyed broker records in both
   places. Started work keeps retrying under its retry policy until it
   succeeds or is dead-lettered. That matches the documented contract.
2. Replaying a dead-lettered keyed invocation whose deadline has passed is
   refused with `409 keyed_replay_expired` (`state.ErrKeyedReplayExpired`,
   the error keyed handler replay already uses). Replay-all leaves such
   entries in the queue: they are excluded from its candidates, so they
   never fill its page. To deliver the event again, publish it again; that
   gives it a fresh deadline.

Broker receipt replays (`trigger_record` entries) keep ADR-609's behaviour:
the replay is accepted, and the row expires if its deadline has passed.

## Consequences

- A keyed delivery that started is never dropped silently. Its failure ends
  in the dead-letter queue.
- A key can stay held past its deadline while its head retries. The
  delivery's attempt budget and its backoff, capped at 30 s for keyed work
  by ADR 20261029190000, bound how long that lasts.
- Deliveries that never started still expire on time, so a backlog of stale
  work is still discarded.
- The replay refusal turns a silent loss into an explicit error that tells
  the caller what to do.
- No schema change. An older scheduler still expires started rows; the
  last one to run decides for a given row until the release completes.
