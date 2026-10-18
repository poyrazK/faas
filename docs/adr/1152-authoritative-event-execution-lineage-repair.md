# ADR-1152: Authoritative legacy event execution lineage repair

Status: Accepted
Date: 2026-10-13
Related: ADR-969, ADR-1151

## Decision

Extend requested forward-only reconciliation with bounded lineage repair. Keep
its durable cursor, cumulative receipt counters and last scan timestamp in one
operational JSON column on the existing coverage row. PostgreSQL locks coverage
rows with SKIP LOCKED, locks retained source receipts against deletion/update,
and locks each matching invocation incarnation while recording its root.
Root associations and progress commit in one transaction. The memory store uses
its existing mutex. A failed transaction or process restart cannot skip committed
repair work. Ready reconciliation continues independently of repair queue order.

Each scheduler pass examines at most the centrally defined receipt batch (100)
across all selected repair jobs. Last-scan ordering prevents repeatedly blocked
jobs from monopolizing that batch. Captured ordinary recipients and authoritative
materialized backfill membership can supply admission evidence. Workflow and
object-notification recipients are excluded. Delivery must already be enqueued.

Require the stable account/source/event/subscription-derived delivery identity,
matching invocation account/app, an original root rather than a replay child,
creation no earlier than receipt acceptance, ordinary async POST delivery shape,
and matching event JSON content. JSON numbers are compared without float rounding.
Do not trust invocation headers as ownership proof. Competing retained acceptances
for the same identity that predate this root, or conflicting existing associations,
remain conflicts. Missing or incompatible executions remain unresolved. Root
association never overwrites an incompatible owner, acceptance or incarnation.

Repair establishes authoritative lineage only: it does not reconstruct earlier
attempt/completion outcomes or execute customer code. Subsequent transitions of
mapped roots and their incarnation-linked replay descendants use the existing
ledger. Loss boundaries stay intact. Only the separate reconciliation completion
can establish a new known baseline; older windows remain unknown.

## Interface and limitations

The existing reconciliation API, CLI and Go/Node/Python responses expose cumulative
scanned, newly repaired, unresolved and conflict counts. Counts describe scanned
receipts, not unique events, executions or current blockers. Matching associations
already recorded by admission are not counted as new repairs. The existing live
blocker predicate and request status remain the readiness signals.

The scan is forward-only through retained enqueued receipts. It does not invent
missing snapshots, resurrect deleted roots, repair unrelated app invocation
sources, or clear ambiguity by choosing an acceptance. Unresolved historical
records can still block reconciliation indefinitely. New normal admissions retain
their existing transactional root association. Payloads are compared in memory
and are not copied into repair progress or returned in diagnostics.
