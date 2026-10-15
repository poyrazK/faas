# ADR-1156: Retained lineage diagnostic backfill

Status: Accepted
Date: 2026-10-13
Related: ADR-1151, ADR-1152, ADR-1153, ADR-1154, ADR-1155

## Decision

Provide a bounded diagnostic scan for legacy receipts that lack individual lineage
case records. Store the latest scan under `case_backfill` in the existing
operational `lineage_repair_progress` JSON. Add one nullable operational case column, `diagnostic_id`, through an append-only
migration; no new table is needed. GET requires read scope; POST requires
existing deploy/write scope and MFA, plus an existing execution coverage row.
Absent coverage returns not_requested on GET and 404 on POST rather than changing
execution-health baselines to create diagnostic state.

POST a nonzero request UUID and, when replacing a completed/expired scan, the
latest request UUID in `expected_previous_request_id`. Serialize intent under the
coverage lock. Repeating the current UUID with its original previous-request guard
returns current progress. A stale guard or replacement of an active scan returns
409. Only the latest scan is retained, and superseded requests fail closed through
the previous-request guard. Deliberate rescans use a new UUID; no unbounded intent
or idempotency-history ledger is added to the JSON field.

Capture a fixed upper retained receipt ID for the owning account. A separate
ascending cursor scans ordinary enqueued acceptance/backfill memberships belonging
to the target subscription at or below that ID. Receipt membership and retention
remain live; this is a bounded pass, not a frozen receipt snapshot or proof of
historical completeness. New memberships on already-passed identities or receipts
committing late can be missed until a deliberate rescan. Work has a 30-day deadline;
expired state is visible immediately, while small latest-scan metadata remains
until replacement or coverage deletion.

The scheduler examines at most 100 receipt candidates across jobs per sweep, with
bounded job selection and rotation by the latest scan update. Lock coverage rows
with SKIP LOCKED and source receipts for sharing, then inspect roots under their
existing locks. Reuse authoritative envelope/owner/content/incarnation and competing
acceptance checks in observation mode. Existing authoritative admission lineage
can be recognized after invocation retention. A provable unassociated root yields
an unresolved case with `lineage_association_missing`; observation never associates
it. Missing/incompatible evidence and ownership conflicts stay reason-coded.

Create only missing unresolved/conflicting case rows, with ON CONFLICT DO NOTHING.
Preserve existing rows even if their retention has elapsed and they await physical
cleanup. Record counters and cursor in the same transaction as new cases; failures
roll back both. Memory equivalents use the existing mutex. Scanned decomposes into
recorded, existing and associated; recorded decomposes into unresolved and conflicts.
These are receipt observations and inserts, not live execution/blocker totals.

New diagnostic cases receive an independent random nonzero UUID in `diagnostic_id`.
Preserve it for the entire case lifetime, including normal repair updates and
explicit retries. Such cases require `expected_diagnostic_id` in individual retry
requests and bulk selections. Legacy cases retain NULL and their existing contract.
Revision and diagnostic identity must both match before evaluation. Saved individual
results already include the immutable diagnostic identity, so duplicate requests
validate that original guard before returning the saved result. Bulk idempotency
compares it as part of the fixed selection. After physical pruning, a freshly
backfilled case gets a new UUID even if outbox, acceptance and numeric revision
repeat. An old or missing diagnostic guard fails with 409 before any association.
After pruning, a later scan may create a fresh case lifetime from remaining source
evidence. Operators must review current case guards and use new retry UUIDs;
expired/superseded case guards and results do not describe that fresh observation.

Wait in `waiting_for_retry_jobs` while a retained pending bulk lineage retry job
exists for the subscription, so diagnostics do not replace a case incarnation
referenced by queued work. The bulk worker rechecks its retention after acquiring
coverage, closing the deadline/lock-wait race with this scan. Individual retries
already serialize through coverage. This wait reads job rows without locking them,
avoiding an inversion with the bulk worker's job-then-coverage lock order.

Diagnostic checkpoints update only the nested JSON field. Normal repair checkpoints
merge their counters/cursor while preserving diagnostic metadata. Neither diagnostic
request nor scan changes forward repair progress, its last-scan time, collection
boundaries, historical outcomes or loss markers. Apply the diagnostic-ID migration and deploy the updated scheduler and
API writers before requesting scans; older binaries replace unknown progress JSON
and must not continue writing during rollout of this capability. Older API
binaries also lack diagnostic-ID guard enforcement; upgrade all API owners before
creating diagnostic cases. Downgrade
refuses to remove diagnostic identities while guarded cases or retry jobs remain;
stop diagnostic requests and physically prune that guarded data first.

## Consequences

Older retained gaps become inspectable through existing case listing, guarded
individual retries and bulk retry preview. Known associations and existing case
records are skipped; payloads and handler errors never enter scan metadata or case
responses. Case retention remains 30 days from observation. Missing/pruned history
cannot be recovered, and case status remains its last recorded evaluation. Normal
repair and separately authorized retries may change lineage independently; this
capability alone never executes handlers, repairs roots or establishes completeness.
