# ADR-20261103222240: Bounded scans and manual verification attempts

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04 (Europe/Istanbul)
- **Amends:** ADR-1591

## Context

A verification query could produce 2,001 rows even when the client stopped at
its byte budget. Draining unread results containing valid one-megabyte rows hit
the query timeout and aborted the source transaction, preventing durable progress.
Manual proof identity also depended on the mutable pipeline revision, so reader
previews discarded completed ranges. Pause/resume additionally cleared progress
while an existing verification request remained outstanding.

Single-table repair temporarily withdraws the publication's last table. The
publication validator scanned a NULL boolean aggregate for this legitimate empty
set and kept recovery stuck before clearing and republishing. Reader validation
also accepted malformed UTF-8 that JSON persistence changed before approval.

## Decision

Use a bounded ordered list of tuple locations within the existing SQL statement
snapshot, followed by recursive, cumulative byte accounting. The location list
contains at most the existing row limit plus one and uses constant-time tid array
lookup. Encode rows only until the byte budget is reached, allowing one first
row above the chunk budget but within the existing row limit. Evaluate at most
one additional row on the server and exclude it when it exceeds the byte budget.
Return an explicit indication of remaining tuples so an extra destination row
cannot be hidden by truncation. Bound the separately returned key tuple as well.

The existing source and target SHARE locks and statement snapshots protect these
transient tuple locations. No tuple location is persisted, no new source helper
or index is installed, and storage/schema/coverage fences remain required. Row
encoding and its proof version are unchanged; existing valid ranges can resume.
Keep all existing row, key, byte, range and timeout limits.

Persist an optional ManualAttempt in verification progress, seeded from the
revision of an explicit manual verification request. Preserve it and the existing
progress through reader edits and pause/resume. Only an explicit new check or
resync clears/replaces that request metadata. Schema workflows continue using
their approved change and retry identity. Older progress without this field uses
the legacy revision fallback and adopts request metadata on reconciliation; an
intervening revision before adoption can safely start one new proof. The optional
JSON field requires no catalog migration. Catalog lease/revision fencing remains
independent of proof identity.

Accept an empty publication only when its exact expected membership is empty.
Retain publication ownership, flags, row-filter, column-list and nonempty
membership validation. Single-table repair otherwise uses the existing native
retirement, clear, copy, verification and refresh stages.

Share UTF-8-aware subject validation across creation and reader changes before
JSON hashing or persistence. Valid Unicode subjects retain their exact values.

## Validation and scope

PostgreSQL regression tests commit consecutive ranges from 2,002 one-megabyte
rows within existing transaction budgets, retain an individually allowed row
larger than a chunk, and detect a trailing row beyond that byte boundary. Native
tests complete single-table repair and continuing CDC, preserve a partial manual
proof across reader edits and pause/resume, and require fresh proofs for new
checks and cancellation retries. Tests also reject malformed UTF-8 before intent
mutation while approving valid Unicode. Composite-key, nullable JSON, backfill,
verification-limit and full native acceptance checks remain required, alongside
race detection, generated SQL, build and repository checks. Production admission
and provider qualification remain unchanged.
