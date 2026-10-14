# ADR 1221: Native application bulkhead qualification

Date: 2026-10-13
Status: Accepted

## Context

Retry budgets and circuit breakers limit dependency pressure, but do not prove
whether active or queued work can exhaust application resources or starve healthy
routes. Existing platform limits remain separate from the application fixture.

## Decision

Add a twenty-third native case with a FIFO bulkhead shared within one dedicated
read caller process. Permit two active dependency calls and two queued requests.
Queue waits expire after one second, excess requests are rejected immediately,
and attempts/overall requests have 2.5/3.5-second deadlines. Every queue/deadline
timer is cleared on completion, cancellation and shutdown.

Use the existing verified TLS loopback SSLRequest fault. Cancel one queued caller
while two permits are held; require its queue entry and timer to retire. Send six
requests, requiring two dependency failures, two queue timeouts and two rejections.
Require three responsive healthy-route probes while both dependency permits are
occupied. Clear the fault and restore two successful reads with matched permit
grant/release totals and full recovered capacity.

Bind exact caller/provider counts, active/queued observations, health latency,
stable identities and joined resources to a run-bound proof. Independently verify
backend retirement after phases and cleanup. Private controls stop the fixture
but cannot manipulate capacity. No platform quota, schema or reliability policy
changes. A portable regression checks FIFO permit handoff and shutdown while
active and queued requests remain.

Raise the gate to 23 cases and verdict version 14. Local PostgreSQL tests execute
exact sources and proof verification; native x86_64 KVM qualification remains
required. Scope is one application instance rather than a platform-wide bulkhead.

## Consequences

The gate detects queue growth, permit leaks and healthy-route starvation during
a dependency failure, with explicit deadline and cancellation evidence.
