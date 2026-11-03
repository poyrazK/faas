# ADR: The drain's walk resumes where the last tick stopped

Date: 2026-11-01
Status: Accepted
Amends: [ADR-20261027160930](20261027160930-continuous-fair-drain.md) (the walk of the due backlog)
Related: [ADR-20261031100001](20261031100001-event-delivery-failures.md) (held apps step aside), [ADR-134](134-durable-async-contract.md)

## Context

The 2026-11-01 blocker hunt (event-driven GA, hunt #6) found one app's
backlog hiding every other tenant's due work on a node.

- Each tick pages the due backlog in `(due_at, id)` order and stops after 32
  pages of 64 rows: 2,048 rows from the head.
- A row the tick cannot dispatch stays `pending` with an unchanged due_at: a
  row of an account at its async cap (left pending on purpose), a row past
  its app's share that no slot took before the tick's wait ended, a FIFO
  queue message behind its app's busy lane, a long-timeout row with no
  background slot.
- So the next tick listed the same 2,048 rows again. One Hobby app with
  5,000 due async invocations and slow handlers, or an account at its async
  cap, kept every other tenant's due rows on the node (async invocations,
  event deliveries, delayed tasks, drain-path crons) out of every walk until
  its backlog fell below 2,048 rows, which can take hours.

ADR-20261027160930 promised that a slow app delays only itself. That held
for rows the walk listed, not for rows behind 2,048 of one app's rows.
ADR-20261031100001 moved a budget-held app's rows back a minute for the same
reason; that rewrites each row, so it suits a rare hold, not every tick.

## Decision

1. **A tick's walk has two parts.** The first 8 pages (512 rows) start at
   the head of the due backlog every tick, so the oldest rows and each app's
   earliest queue messages get a chance every tick. The other 24 pages
   (1,536 rows) resume after the last row the previous tick's walk listed.
2. **A round ends at the tail.** When a walk lists the last due row, the
   next tick's resumed part starts again right after the head part. Every
   due row of the node is listed at least once per round, which takes at
   most ⌈backlog / 1,536⌉ + 1 ticks. The scheduler keeps the position in
   memory; a restarted schedd starts a new round.
3. **FIFO lanes keep their order across the round.** When a walk lists an
   app's FIFO queue messages but cannot start its lane (the lane from an
   earlier page or tick still runs), the app's later messages in the resumed
   part of the round wait for the next round, which lists the skipped ones
   first. The head part lists each app's earliest messages, so it always
   may start a lane.
4. The walk's per-tick cost is unchanged: at most 32 pages, the same query
   and the same `invocations_drain_due_idx` keyset scan. No rows are
   rewritten and no migration is needed.

## Consequences

- Another app's due row behind a backlog the node cannot dispatch is listed
  within one round instead of never. With a 5,000-row backlog that is at
  most 5 ticks.
- The bound grows with the node's due backlog: a million due rows on one
  node take about 650 ticks per round. Plan caps bound a backlog, and such a
  backlog is an incident in its own right.
- A FIFO app whose messages sit behind another app's backlog advances one
  lane run per round while its earliest message is outside the head part.
- The share, the lanes, the 2 s tick wait and the long-dispatch slots are
  unchanged.

## Rejected alternatives

- **A per-app cap in the due query** (a window function limiting rows per
  `app_id`). Rows past the cap are still traversed by the `(due_at, id)`
  scan, so skipping a million-row backlog costs a million index and heap
  visits per tick; capping the traversal brings the starvation back.
- **An app-leading index and a loose index scan over apps.** It gives a
  constant bound, but needs a migration on the largest hot table, changes
  the walk from due order to app order, and probes every app with pending
  work on every tick.
- **Deferring rows the tick could not claim** (moving their due_at, as a
  budget hold does). It rewrites up to 2,048 rows a tick, fires the
  `invocation_due` trigger for each and overwrites `last_error`.
