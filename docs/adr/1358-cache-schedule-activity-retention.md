# ADR-1358: Cache schedule activity retention and explicit cursor recovery

Status: Accepted

## Context

The durable event feed grows indefinitely. Cleanup must retain its ordered cursor
semantics and cannot silently convert lost events into a successful empty page.
Current schedule discovery is live pagination, not an atomic history snapshot.

## Decision

Retain a 30-day event window, with eventual bounded prefix cleanup in apid. Keep a
nondecreasing pruned-through sequence on the existing counter, independent of the
surviving event count. Lock at most 20 counters with SKIP LOCKED per maintenance
transaction and delete at most 1000 expired prefix events for each. Stop at the
first nonexpired event, including when clocks move backward. Advance the floor
and delete records in the same transaction. Serialize with writers on their
existing counter lock; read floor, head and page in one repeatable-read snapshot.
Prioritize the oldest eligible prefix to avoid starvation by environment UUID.

Cursors strictly below the floor return HTTP 410 `cursor_expired`, including an
omitted cursor interpreted as sequence zero. The exact floor remains valid.
Keep monotonically allocated sequences even after deleting all events. Expiry
metadata carries scoped floor and observed-head cursors and the retention window.
MCP validates these against current account, immutable target and consent.

Recovery saves the head cursor BEFORE reading a live schedule baseline, then
consumes events after that head. This can duplicate baseline observations, which
consumers reconcile by schedule identity; it does not drop concurrent transitions.
If cleanup overtakes recovery, return expiry again. There are no consumer leases
or pinned cursors and no fabricated reconstruction of removed historical outcomes.
Schedule action receipts remain independent of ledger retention and gateway
convergence remains independently observed.

## Consequences

Cleanup backlog or writer contention can extend retention beyond 30 days. Batch
bounds constrain work per transaction, not total event cardinality or scan cost.
Install migration 20261016153051026 and upgrade all apid readers/writers before
consumers rely on the protocol. Old readers detect gaps as conflicts rather than
exposing recovery metadata. Rollback of pruning restores both floor and events.
The native Problem extension is available to Go, Node and Python clients.
