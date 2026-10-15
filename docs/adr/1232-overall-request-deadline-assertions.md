# ADR-1232: Overall request-deadline assertions

Date: 2026-10-13
Status: Accepted

## Context

A bounded queue wait or retry average does not bound the total response time seen
by a user. A percentile also hides individual late requests. A client-side abort
must not be accepted as evidence that an application returned its timeout contract.

## Decision

Add phase-local `request_deadlines` maps, also available in reusable assertion
profiles. Each named request declares `within`, positive `min_samples`, and an
optional phase-local `expect` response contract. Without an override it retains
the original request's contract. Bind names to exactly one load request.

Create a child context whose deadline begins at the measured request start and
covers preparation, dispatch and bounded complete response-body consumption.
Require every selected request to complete its expected contract and the observed
maximum latency to stay within budget. Insufficient samples, missing latency
measurements, wrong contracts, body stalls and client cancellation fail.

Reuse the dispatch gate to drain admitted HTTP requests before phase transitions,
so clearing a fault cannot turn an in-flight expected timeout into a success.
Record actual phase boundaries; a drain can shorten the following scheduled
window. Expanded schedules and effective deadline budgets remain replay-bound.

The sealed pool fixture optionally uses one 500 ms application timer across permit
acquisition and dependency I/O. It joins the waiter or real TCP socket before
returning HTTP 504 with a fixed JSON timeout contract. Request disconnection
aborts that same downstream work. Existing leak assertions independently verify
the final application-reported socket baseline after load or cancellation.

## Consequences

These are client-observed request budgets within timed phases, not an arbitrary
workflow or service trace deadline. They include transport overhead. A percentile
or a test-client abort cannot substitute for a complete timeout response. Terminal,
HTML, JSON and JUnit evidence retain per-route maxima, limits, sample counts,
response failures, expected status and verdicts. Resource counts remain
application-reported; native qualification requires the designated KVM host.
No platform API, database schema, SDK or metric label changes are introduced.
