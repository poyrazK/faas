# 1019: Bound and verify retries per logical test request

Date: 2026-10-13

Status: Accepted

## Context

A phase-wide average can conceal a single request that retries excessively.
Attribution must not introduce unbounded identifiers into Prometheus labels or
silently certify incomplete gateway evidence as zero attempts.

## Decision

Add HTTP `dependency_attempts.per_request: true` to reusable assertion profiles.
Use an execution-specific 16-hex scope, a registered run ID and canonical slots
1 through 1024. The host assigns IDs only to the selected incoming route during
its fault phase. Applications propagate `X-Gregale-Test-Request-ID` on selected
dependency calls and report the final number of completed attempts through
`X-Gregale-Test-Dependency-Attempts` before completing the incoming response.

Drain requests and pause dispatch while strict schedules change fault plans,
so baseline or previous-phase calls cannot accidentally enter a new scope.
Resume requests when the next phase is active, before collecting retired-plan
counters. Cancellation and setup errors release the dispatch gate.

Only full-coverage HTTP latency/status rules with an explicit source may carry
`correlation_scope`. The gateway observes after normal routing/identity policy
checks and uses the existing asynchronous scenario recorder. Store hashed
per-slot counters, a support/completeness counter and one uncorrelated counter
in the existing generation-scoped match table. Existing namespace authorization,
plan replacement and cleanup own these rows. No schema migration or per-request
Prometheus label is introduced. Arbitrary, malformed, foreign and stale IDs map
to the single uncorrelated counter instead of creating additional keys.

Cross-check gateway counts against every incoming completion receipt and total
matched calls. Missing support, propagation, receipts, late records, unassigned
traffic and capacity overflow fail. Limits must be positive integers. Report
coverage, maximum, a bounded histogram, violation count and at most 100 violating
request IDs in JSON, HTML, JUnit and terminal output. Runtime scopes are excluded
from configuration fingerprints; the strict option and effective limits remain
bound by replay hashes.

## Consequences

This qualifies instrumented HTTP calls during one fault phase. TCP streams cannot
carry HTTP request IDs. Applications must join selected outgoing work before
reporting completion; generic uninstrumented apps cannot claim strict coverage.
A phase with no independently observed dependency activity cannot establish
correlation support. Aggregate limits remain unchanged and available without
instrumentation. Native end-to-end qualification requires the updated gateway
on the designated KVM fleet.
