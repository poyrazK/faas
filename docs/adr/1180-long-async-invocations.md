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

3. **Leases outlast the run.** A long-timeout invocation is claimed for
   the wake allowance (60 s) plus its timeout plus a 30 s margin
   (`api.AsyncInvocationLease`). Its dispatch is bounded by the wake
   allowance plus the timeout, so the outcome is recorded before the lease
   could lapse. Named queue claims take the longer of 600 s and the batch
   timeout plus the margin.

4. **Long runs do not hold shared loops.**
   - The drain delivers a long-timeout invocation in the background, under
     a drain-wide cap of 256 (`DefaultDrainLongDispatches`). A row beyond
     the cap stays pending for a later tick. Each app's FIFO queue
     messages still go one at a time, in order.
   - The cron loop leaves a long-timeout function's fire to the drain
     instead of delivering it inline.
   - Named queue triggers already run on their own lanes.

5. **Batches fit one whole record.** A trigger batch for a long-timeout
   app may take the longer of the 5-minute batch bound and one whole
   record (`api.TriggerBatchDispatchTimeout`); schedd waits that long.

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
- A schedd restart while long invocations run leaves them `dispatching`
  until their leases lapse, up to the timeout plus 90 s later, when they
  are requeued. The delivery contract was already at-least-once.
- A long-timeout cron fire is delivered by the drain, so a transient
  failure is retried under the drain's retry policy rather than waiting
  for the next schedule boundary. This was already the case when the
  drain won the claim race.
- A trigger batch for a long-timeout app holds its lane for up to one
  record's run; later records in that batch are retried
  (`batch_timeout`), as before.

## Alternatives

- **Renew leases while the invocation runs.** The operation path already
  renews (`operationClaimContext`). Rejected for this change: every
  running invocation would write every 20 s, and a lease sized to the run
  needs no writes. Renewal can replace it if timeouts outgrow 15 minutes.
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
