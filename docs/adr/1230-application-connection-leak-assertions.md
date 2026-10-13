# ADR-1230: Application connection-leak assertions

Date: 2026-10-13
Status: Accepted

## Context

Connection attempt ratios can pass while a client retains sockets after a fault.
Current gateway counters measure attempts and injected faults, rather than an
application-wide live socket gauge. Opaque TCP streams cannot report client pool
ownership or safely infer an application's total connections.

## Decision

Add optional staged `load.connection_leaks` to TCP fault/recovery schedules.
Poll a side-effect-free application-relative endpoint at 100 ms intervals. It
returns a nonnegative integer `active_connections` and a canonical nonzero UUID
`instance_id`, stable for the observed application instance. Bound each request to
250 ms, each response to 4 KiB, and stored observations to 4096. Optional `as`
uses an existing declared consumer; credentials and bodies are not reported.

Use the minimum observed initial baseline and retain it for every cycle. Record
sampled peaks and remaining counts. During recovery, allow explicit `max_extra`
for normal in-flight calls; return within `within`, sustain for `sustain`, and
remain within budget through the end of the observed recovery window. Missing,
sparse or invalid observations, changed identities and renewed leaks fail.

After load workers and fault cleanup join, independently poll again with a bounded
context that survives cancellation. This post-drain check requires the original
baseline with no extra allowance. An interrupted campaign remains failed even
when its post-drain check passes. It does not convert cancellation into success.

## Consequences

Counts are application-reported samples, not a gateway-wide socket census or proof
against dishonest instrumentation. They must describe the same dependency scope
and include idle retained client sockets. Warm legitimate connection pools before
measuring the initial baseline. Multiple unaggregated application instances fail
identity checks. A sampled peak may miss activity between polls.

Each recovery contributes resource evidence to the cycle verdict. JSON, JUnit,
HTML and terminal reports retain baseline, peak, remaining counts, deadlines,
identity, the first failed resource cycle and the separate post-drain result.
The opt-in echo fixture tracks its real sockets until close and joins them on
request disconnection. No database table, platform API route or metric label is
added. Native VM acceptance still requires the designated x86_64 KVM runner.
