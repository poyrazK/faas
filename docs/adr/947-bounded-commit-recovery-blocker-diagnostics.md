# ADR-947: Bounded recovery blocker diagnostics

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-617, ADR-938, ADR-945, ADR-946

## Context

Recovery status distinguishes delivery evidence from acknowledged replay execution,
but operators also need to understand why current routing is waiting.

## Decision

Add opt-in CLI --diagnostics, Go watch options Diagnostics, Node diagnostics,
and Python diagnostics to recovery status monitoring. Go, Node and Python expose
separate diagnostic enrichment helpers for freshly validated status results.
Node/Python plain status helpers also accept diagnostics; the Go CLI enriches
a successful ordinary status result. Existing calls keep diagnostics disabled.

Each enrichment clones its input and samples one subscription-filtered backlog
page per distinct observed subscription, with at most 50 reads and 200 recipient
rows per page. Reads share the invocation's cancellation/deadline; standalone
helpers rely on caller-provided cancellation/timeouts. Queries use the configured
client and generated endpoint, never saved receipt/action URLs. Unobserved or
failed receipt reads have unavailable diagnostics. Workflow rows cannot match an
application diagnostic. Do not carry saved diagnostics through resume validation.

Require exact event source, event ID and acceptance timestamp, plus matching
subscription ID and application consumer kind, before attaching a recipient.
The separate diagnostics object includes status, observed_at, coverage,
truncated, and the matched recipient. That recipient exposes waiting_reason,
ordering_blocker, capacity_scope, next_attempt_at, lease_until and routing state.
Subscription controls include paused/rate-limited consumers and circuit states.
The backlog's documented precedence governs the reported waiting reason.

Diagnostic statuses are available, unavailable, not_found, read_error and
budget_exhausted. Truncated describes the independently bounded recipient page;
consumer pagination is not consulted. Require the expected backlog coverage and
an observation timestamp. Not_found only means no exact recipient appeared in
this page: active work may have left the routing backlog, retention may have
removed evidence, or the row may be beyond the page. Never infer ready, expired,
terminal or no blocker from absence. No pagination or retries are performed.

Diagnostic read failures are explicit and nonfatal; they do not consume receipt
read retry budgets or modify delivery status, attribution, counts, completeness,
freshness, comparison changes or settlement. Overall watch timeout/cancellation
still stops monitoring. Samples may describe current routing after the observed
receipt generation; they are not proof about the acknowledged replay execution.
Watch checkpoints and callbacks include diagnostic snapshots when enabled.

## Consequences

Operators can inspect independent consumer blockers while retaining unknown
submission semantics and bounded reads. Backlog pages may miss busy recipients;
future exact-recipient queries can improve coverage. This is read-only and does
not submit recovery or change server behavior. Production pagination, retention
and concurrent routing qualification remains pending. No local tests are added
or run; compliance registries are unchanged.
