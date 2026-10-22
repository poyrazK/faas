# ADR-1180: Long async, queue and cron invocations

- **Status:** accepted
- **Date:** 2026-10-12
- **Related:** ADR-1150 (per-plan request timeouts), ADR-134 (async
  invocations), ADR-100 (triggers), ADR-987 (binary runtime), spec §4.3,
  §4.10

## Context

ADR-1150 lets a function answer an HTTP request for up to its plan's
ceiling: Free 30 s, Hobby 1 min, Pro 5 min, Scale 15 min. It left async,
queue and cron invocations at 30 s. A synthetic invocation carries no
request budget, so the runner fell back to its 30 s default. The
dispatchers around it also assumed short work:

- the drain claimed an invocation with a fixed 60 s lease, and the
  expired-lease sweep requeues a `dispatching` row whose lease lapsed. A
  longer run would have been delivered a second time while it still ran;
- the drain's tick waits for every dispatch in its batch, and cron fires
  are delivered one after another on the scheduler loop. One long
  invocation would have held async delivery, or every other cron, for its
  whole run;
- schedd's gateway clients cut every call at 30 s (60 s for trigger
  batches), the gateway cut each trigger batch record at 30 s, and named
  queue triggers claimed their records for 600 s.

Background work is where long runs belong: reports, exports, batch
processing.

## Decision

1. **One timeout per function.** An async, queue, cron or replay
   invocation may run for the app's `request_timeout_s`, clamped to its
   plan's ceiling and never less than 30 s
   (`Limits.AsyncInvocationTimeout`, `state.AsyncInvocationTimeout`).
   An app without its own timeout keeps 30 s. No new setting: the timeout
   that bounds a function's HTTP requests bounds its background work too.

2. **The guest is told.** gatewayd-internal stamps that timeout as the
   request budget of every synthetic invocation, so the forwarder sends it
   as `X-Faas-Deadline-Ms` (ADR-1150) and the runner allows the run.

3. **Leases are renewed while the run lasts.** The drain claims a
   long-timeout invocation with its ordinary 60 s lease and renews it
   every 20 s while the run is delivered (`RenewInvocationLease`), as the
   operation path already does. A scheduler that crashes or restarts
   stops renewing, so its invocations are requeued within one lease; their
   interrupted deliveries end with the scheduler's connection. Renewal is
   fenced by the claim's attempt and refuses a lapsed lease. The keeper
   tolerates one failed renewal; when the row is no longer its attempt's,
   or the lease would lapse, it cancels the delivery so a requeued attempt
   cannot overlap it. A store without renewal claims for the wake
   allowance plus the timeout plus a 30 s margin
   (`api.AsyncInvocationLease`). The dispatch is bounded by the wake
   allowance plus the timeout. Named queue claims take the longer of 600 s
   and the batch timeout plus the margin.

4. **Long runs do not hold shared loops.**
   - The drain delivers a long-timeout invocation in the background, under
     a drain-wide cap of 256 (`DefaultDrainLongDispatches`). A row beyond
     the cap stays pending for a later tick. Each app's FIFO queue
     messages still go one at a time, in order.
   - The cron loop leaves a long-timeout function's fire to the drain
     instead of delivering it inline.
   - Named queue triggers already run on their own lanes.

5. **Long-timeout triggers deliver one record per batch.** A trigger
   batch for a long-timeout app may take the longer of the 5-minute batch
   bound and one whole record (`api.TriggerBatchDispatchTimeout`); schedd
   waits that long. Such an app's triggers poll one record per batch, as
   named-queue push lanes already did, because records behind a long one
   would time out without running and spend their attempts. The cap
   applies before the poll: a record polled but left out of a batch stays
   claimed until its lease or visibility timeout lapses.

6. **Default apps are unchanged.** Every path above keeps its previous
   bound and inline delivery for an app at the 30 s default, and resolving
   the timeout costs no lookup for it.

7. **The bridge waits for the guest's answer.** vmmd's stream bridge
   waited at most 30 s for the guest's response headers, then answered
   502. That also cut ADR-1150's synchronous requests whose handler
   answers once, without streaming, after more than 30 s; ADR-1150's
   metal test streamed from the start and so never reached it. The bridge
   now waits as long as the request's `X-Faas-Deadline-Ms` allows, by the
   same rule as the runner (`api.GuestDeadline`: at least 30 s, at most
   15 min). A request without the header keeps 30 s.

## Consequences

- A Scale function can process a queue message, a cron fire or an async
  invocation for up to 15 minutes; Pro for up to 5.
- A schedd restart interrupts the long invocations it was delivering;
  they are requeued within one 60 s lease and run again. The delivery
  contract was already at-least-once.
- Each running long invocation renews its lease every 20 s: one indexed
  row update.
- A long-timeout cron fire is delivered by the drain, so a transient
  failure is retried under the drain's retry policy rather than waiting
  for the next schedule boundary. This was already the case when the
  drain won the claim race.
- A long-timeout app's trigger throughput is one record per batch per
  lane; its records run one after another rather than timing out in a
  shared batch.

## Alternatives

- **A lease sized to the whole run.** It needs no renewal writes, but a
  crashed or restarted scheduler's invocations sat `dispatching` for up to
  the timeout plus 90 s before being requeued, though nothing was running
  them. Kept only as the fallback for a store without renewal.
- **Make every drain dispatch asynchronous.** Rejected: it changes the
  delivery timing of every app to serve the few with long timeouts.
- **A separate async timeout setting.** Rejected for the same reason as in
  ADR-1150: `request_timeout_s` already exists end to end and means "how
  long this function may run".
- **Keep Go and binary handlers warm across restores.** Considered in the
  same change and rejected: a handler captured warm resumes with the
  random state it held at capture, shared with every sibling restored from
  the same snapshot (GHSA-24j2-p895-mwc9, ADR-987 §5). Go's runtime RNG
  cannot be reseeded in place, so the restart stays. It already runs in
  the background after the restore barrier.
