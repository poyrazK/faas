# ADR: The invocation drain serves apps fairly and never waits on a page

Date: 2026-10-27
Status: Accepted
Amends: [ADR-1180](1180-long-async-invocations.md) (its rejected "make every drain dispatch asynchronous")
Related: [ADR-614](614-event-delivery-backpressure.md), [ADR-191](191-scheduler-divergence-and-bounded-dispatch.md), [ADR-972](972-parallel-queue-push-dispatch.md)

## Context

A GA scale blocker from the 2026-10-25 event audit: one slow consumer
blocked its node's drain. Event deliveries are async invocations, so they
run through it.

- A tick listed 64 due rows, handed them to 8 workers and waited for every
  row before listing the next page (up to 32 pages). Rows of an app at the
  default 30 s async timeout (every Free app, and any app without a longer
  `request_timeout_s`) were delivered inline, so one such row held the page
  for its wake plus its run: about 75 s at worst.
- Rows were grouped by app, not interleaved, so a burst at the head of a
  page took every worker before the next app's first row.
- FIFO queue rows ran one at a time across every app in the page, after the
  parallel rows.
- An inline row did not renew its 60 s lease, which a slow wake plus a 30 s
  run can outlast.

ADR-1180 moved long-timeout rows to the background and rejected making
every dispatch asynchronous because it would change every app's delivery
timing to serve the few with long timeouts. Fairness for default apps is a
different reason.

## Decision

1. **One pool for every tick.** The drain's inline dispatches share one pool
   of `dispatchConcurrency` slots (8 by default) across ticks.
2. **A share per app, work-conserving.** An app may always hold up to a
   quarter of the slots (at least one; 2 of 8). Beyond its share it may take
   another only while more than a share stays free. An app alone on the node
   can use most of the pool, but at least a share is always left for the
   next app.
3. **No page barrier.** A page's rows go round-robin across their apps onto
   the pool, and the walk lists the next page without waiting for them. A
   row whose app is at its share is set aside until the end of the walk, then
   takes a slot beyond the others' share if one frees before the tick's wait
   ends; otherwise it stays pending for a later tick.
4. **A tick returns after a short wait.** A tick waits up to 2 s for the
   dispatches it started. Longer ones finish in the background on their
   slot, and the next tick skips rows still in flight.
5. **FIFO queue messages on a lane per app.** An app's FIFO queue messages
   run one at a time, in order, on one slot of the app's share. Apps no
   longer wait for each other's queues. While an app's lane from an earlier
   tick still runs, its messages wait for a later tick.
6. **Every delivery renews its lease.** The ADR-1180 keeper (renew every
   third of the lease, cancel if it lapses) now covers inline deliveries too.
7. Long-timeout rows keep their background path and their 256-slot cap.

## Consequences

- A slow app delays only itself: other apps' rows are dispatched within the
  same tick, and other apps' newly due rows are not held behind its run.
- A burst from one app still runs near the node's full concurrency when
  nothing else is due.
- A tick can return before its deliveries finish. Their outcomes are
  recorded as before; tests that need them wait on the drain.
- An app's FIFO queue may drain one page per tick while it is behind a slow
  message, instead of every listed message in the same tick.
- Each inline delivery holds a lease keeper (a goroutine and a ticker); a
  delivery shorter than a third of the lease renews nothing.

## Rejected alternatives

- **A hard per-app cap.** It throttles a lone app's burst even with idle
  slots.
- **Fair pages with the page barrier.** A slow app no longer blocks other
  apps within a page, but newly due work still waits for the tick's slowest
  row.
- **A goroutine per app.** Unbounded with the number of apps, and still
  needs the shared bound to protect the node.
