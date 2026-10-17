# ADR-1388 · Paginated deferred acknowledgment history

Status: accepted

## Context

The initial history view exposes the latest 100 coalesced outcome groups.
Paging on last_at would skip groups as renewals update their sort position.
Changing the original endpoint ordering would also break its documented view.

## Decision

Add a read-only pages subresource ordered by immutable first_at and the full
outcome key, all descending. Use exclusive keyset continuation, a first-page
cutoff, bounded page size and one-hour cursor lifetime. Bind canonical cursors
to account, app, subscription, normalized filters and effective page size. Check
normal ownership on every read; cursors are not authorization capabilities.
Support delivery, outcome-kind and first-occurrence time bounds. Keep the
original endpoint, response and latest-update ordering unchanged. No additional
table or schema mutation is necessary.

## Consequences

Renewals cannot move rows across page boundaries. Aggregate values remain live;
this does not reconstruct their state at the cutoff. Retention/deletion and
inserts committed after the initial read limit snapshot completeness. Document
these constraints and avoid claiming exact time-window operation rates. Clients
must repeat filters and page size with continuation requests. SQLC queries and
Go, Node and Python clients share the new route and page envelope.
