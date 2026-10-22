# ADR-1231: Connection-pool exhaustion assertions

Date: 2026-10-13
Status: Accepted

## Context

A retry budget or connection-leak assertion can pass while callers wait too long
for a scarce pool connection. Tests must prove that the pool was actually full,
that its queue remained bounded, and that successful throughput returned.

## Decision

Add optional staged `load.pool_exhaustion` for source-scoped TCP fault/recovery
pairs. Poll a side-effect-free application endpoint using the existing bounded
observation model: 100 ms intervals, 250 ms request timeout, 4 KiB responses and
at most 4096 stored samples. Optional `as` uses a declared consumer credential.

The endpoint reports a canonical stable instance UUID, capacity, active permits,
queued requests, cumulative peaks, waiter accounting, maximum completed queue
wait in integer milliseconds, and cumulative successful dependency completions.
Require complete integer accounting and monotonic counters. Changed identity,
missing observations or counter resets fail rather than resembling recovery.

For every fault require positive queued-request growth and a full-capacity peak.
Enforce capacity, queue and wait budgets including cumulative high-water marks.
For recovery require a sustained sequence of successful-completion windows within
the configured deadline, followed by continued healthy windows through phase end.
Scale the required count for windows slightly longer than the configured width.

After load workers and fault cleanup join, independently require zero active
permits and queued waiters, with all created waiters finished. Optional
`min_cancelled_waiters` requires positive cancellation evidence relative to the
initial baseline. Cancellation does not change the interrupted run to success.

## Consequences

Counts are application-reported and must cover the tested pool, rather than a
platform-wide census. JSON, JUnit, HTML and terminal output retain pool peaks,
waits, throughput windows, first failed cycle, and separate post-drain results.
No platform API, database table, metric label or quota is added.

The sealed reference fixture has two permits, two queued callers, a 200 ms queue
deadline, a 750 ms dependency deadline, and no retries. It tracks real TCP sockets
until close, joins queue timers on cancellation, and serves health independently.
Its connection permits are returned after every attempt; it does not model idle
connection reuse, database transactions, or a specific driver's pool semantics.
Local real-stream checks do not replace native x86_64 KVM acceptance.
