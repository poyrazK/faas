# ADR-1405 · Deadline-ordered pending delivery triage

Date: 2026-10-15
Status: Accepted

## Context

Full pending summaries can warn about approaching deadlines beyond the rate
preview's 1,000-row cap, but do not identify affected deliveries. The backlog
listing orders by acceptance time, and the rate preview's bounded risk identities
are tied to its model rather than a complete deadline-ordered traversal.

## Decision

Add a read-only subscription-scoped effective-deadline listing. Reuse the routing
projection, captured effective-deadline function, ownership target and waiting
reason functions. Return event identity and timing metadata without event payloads.
Order by effective deadline, acceptance time and internal receipt identity. Read
at most 100 returned rows with one lookahead row and retain a bounded candidate
buffer in memory. Apply the existing five-second request and four-second statement
budgets; a timeout returns no partial page.

Support absolute inclusive/exclusive bounds plus expired/upcoming classification.
Anchor classification and the acceptance upper bound to the first-page clock;
report expiry flags and waiting reasons at each page's fresh observation. Bind
canonical opaque cursors to scope, normalized filters and page size with a
15-minute lifetime. Keep cursors independent of authorization and recheck access
on every read. Reject unsupported, repeated or inconsistent query parameters.

Document live membership and the possibility of changed, skipped or revisited
rows as retained work changes. Do not imply frozen snapshots, delivery order,
capacity reservations, aggregate health or deadline safety. Effective deadlines
exclude work for which existing age-policy/ordering rules suppress a deadline.

## Consequences

Operators can identify pending deliveries behind deadline alerts without changing
retry, replay, acknowledgement or retention behavior. The endpoint is available
through Go, Node and Python SDKs. No schema migration is required. Query time is
bounded, but finding the earliest matching rows may still scan a large backlog;
callers can narrow deadline bounds and must handle timeouts and live pagination.

See [operation and pagination semantics](../event-subscription-deadline-triage.md).
