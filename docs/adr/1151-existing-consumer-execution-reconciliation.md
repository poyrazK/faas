# ADR-1151: Existing consumer execution coverage reconciliation

Status: Accepted
Date: 2026-10-13
Related: ADR-969, ADR-967, ADR-934

## Context

Subscriptions predating the durable execution-outcome ledger have unknown
baselines. Retained historical invocation metadata cannot prove subscriber
ownership, and declaring those histories complete would produce misleading SLOs.

## Decision

Accept an idempotent, forward-only reconciliation request on the existing
subscription coverage row. A nullable request timestamp is the durable work
record, so retries and scheduler restarts preserve the original request. Newly
qualified subscriptions return `not_required`; repeating a completed request
returns `completed` without changing its collection boundary.

The invocation-retention scheduler processes ready requests in bounded batches,
using row locks with SKIP LOCKED in PostgreSQL and the store mutex in memory.
Blocked requests do not occupy the ready batch or starve other apps. Completion
requires every retained invocation in the app to have authoritative ledger-root
lineage with matching account, app and root incarnation. Because unmapped
executions cannot safely be assigned to a consumer, unrelated app invocations
also conservatively block completion. This first version does not infer ownership
from headers, recreate deleted outcomes, delete blockers, or infer lineage. ADR-1152 adds bounded repair from authoritative retained evidence.

Event routing already associates roots and captures retained prior outcomes in
its admission transaction. Mapped active executions can cross the new boundary;
subsequent transitions append outcomes to their authoritative roots. Unmapped
retained parents prevent legacy replays from crossing reconciliation undetected.
Requests do not alter admission, retry, ordering, replay or deduplication rules.

When the blocker predicate clears, atomically set the ledger baseline known and
its collection timestamp to the database clock at completion. Preserve all loss
high-water marks and the independent retained-current-state coverage fields.
Only windows starting at or after this new boundary can qualify for complete
ledger evidence. Unknown outcomes, retention loss, minimum samples and freshness
still apply. Completion is not a promise of an already eligible SLO window.

GET and POST on the subscription's `execution-reconciliation` resource expose
status, request timestamp, completed collection boundary and the live blocker
predicate. GET requires read scope; POST requires deploy/write scope. POST returns
202 while waiting, otherwise 200. The CLI and Go, Node and Python SDKs expose
these operations. Existing execution-health responses retain their contract.

## Consequences

Existing consumers can opt into provable future windows without being recreated.
Reconciliation adds no new table, lease or daemon. It relies on the existing
retention sweep cadence and durable coverage row. It may wait indefinitely for
unattributed retained app executions; customers can inspect that condition, but
this operation does not recommend deleting evidence to force eligibility. Authoritative lineage repair (ADR-1152) reduces those conservative
blockers when retained evidence is unambiguous. Migration adds one operational column and a pending-request index.
