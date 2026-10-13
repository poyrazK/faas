# ADR 1222: Native combined application resilience qualification

Date: 2026-10-13
Status: Accepted

## Context

Separate application retry, breaker and bulkhead cases do not establish correct
policy ordering when a retry or queued request meets a circuit transition.
Production platform policies remain separate from these application fixtures.

## Decision

Add a twenty-fourth native case in an opted-in combined mode of the existing
bulkhead source context, reusing its FIFO permit implementation. Fix two retry
tokens per 30-second monotonic window, a four-failure circuit threshold with a
three-second cooldown and one half-open probe, two permits, and two queue slots.
Queue/attempt/overall deadlines are 1/1.5/4.5 seconds. Retries have cancelable
100 ms backoff, release their old permit, and acquire a new permit per attempt.

Check circuit state before queueing and again after permit acquisition. Admit
extra attempts against the shared budget only after circuit and permit admission.
Half-open probes never retry or consume retry tokens. Preserve generation guards
against late settlement. Cancel active and queued work before the failure wave
and require no retry consumption; verify overload, healthy-route isolation, two
protected retries, circuit opening, rejected callers, failed/canceled probes,
and successful recovery with the retry budget still exhausted.

Bind caller/provider identities, exact independent attempts, positive SSLRequest
faults, budget window, state transitions, permit protection and matched releases
into a run-bound proof. Join all application and provider resources, then inspect
backend retirement independently. A portable HTTP regression verifies a queued
request rejects a newly open circuit after acquiring and releasing its permit.

Raise the gate to 24 cases and verdict version 15. Local PostgreSQL tests exercise
exact sources and proof verification; designated native x86_64 KVM qualification
remains required. No platform quota, reliability policy, schema or ledger changes.

## Consequences

The gate qualifies policy composition within one application instance, including
queue/circuit races and retry/probe permit accounting, without certifying unrelated
cross-process or fleet-wide production policies.
