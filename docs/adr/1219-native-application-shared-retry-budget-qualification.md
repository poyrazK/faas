# ADR 1219: Native application shared retry-budget qualification

Date: 2026-10-13
Status: Accepted

## Context

Native dependency cases bound individual connection attempts, but concurrent
application retry loops can amplify a failure. Gregale already implements separate
Redis-backed gateway fleet and outbound integration retry budgets; a customer
application fixture should not claim to certify those production mechanisms.

## Decision

Add a twenty-first native case with a dedicated read caller that shares two retry
tokens across its concurrent requests in one process. Fix a 30-second monotonic
window, two attempts per original, a 1.5-second attempt deadline, 100 ms cancelable
retry delay, and a 4.5-second overall deadline. Originals are always outside the
budget. Lazy window expiry requires no replenishment timer; reserved tokens stay
spent if their delay is canceled.

Use the existing provider's verified TLS loopback SSLRequest stall. Cancel one
original before retry admission and require no token consumption. Send six failing
originals concurrently, requiring exactly two extra attempts and per-request counts
1,1,1,1,2,2. Confirm attempts independently at the provider alongside positive
pre-TLS fault and queue evidence. Clear the fault and require first-attempt recovery
while the retry allowance remains exhausted.

Bind caller/provider identities, budget window/configuration, aggregate counters,
joined requests and cleared deadline/backoff timers into a run-bound proof.
Independently verify backend retirement after phases and cleanup; underlying SQL
connections are bounded by existing provider deadlines after HTTP cancellation.
Private namespace-bound controls stop the caller; they cannot reset its budget.

Raise the native gate to 21 cases and verdict version 12. Local real PostgreSQL
tests exercise exact sources and proof validation; portable cancellation checks
also join a reserved backoff without issuing its second request. Qualification
remains scoped to an application process and requires designated native x86_64 KVM.

## Consequences

The test detects concurrent application retry amplification without introducing
another platform retry policy. Cross-process Redis coordination remains covered
by existing gateway and outbound fleet canaries, rather than this fixture.
