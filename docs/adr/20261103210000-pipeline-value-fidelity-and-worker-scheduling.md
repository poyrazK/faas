# ADR-20261103210000: Pipeline value fidelity and bounded queue scheduling

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-03
- **Amends:** ADR-1591

## Context

Converting a record directly to JSON merges SQL NULL with JSON literal null.
Reconstructing it with jsonb_populate_recordset can silently replace a JSON null
with SQL NULL, and the same lossy digest then incorrectly certifies equality.
Conversion through jsonb also changes the representation of json columns.

PostgreSQL table inheritance can expand publication membership to unselected
descendants. Those tables can lose the ability to update or delete when they
lack a usable replica identity. The pipeline contract supports ordinary tables.

The isolated worker previously advanced one pipeline, check and setup per
second in a single loop. Even fast jobs could not refresh 300 admitted pipelines
within the 90-second observation window. A slow remote call stalled every queue.

## Decision

Encode each verification row as an ordered array of nullable text values under
the existing canonical SQL settings. SQL NULL is a null array element; every
nonnull value, including JSON literal null, is a string. Hash and capture the
same bounded bytes. Restore only primary keys and approved new columns with
their builtin SQL input types; compare nullable text when deciding whether to
update. Keep existing range, row, byte, lock, lease and read-gate limits.

Bind the row encoding version to physical verification run IDs so old ranges
and saved proofs cannot complete a resumed run under the new algorithm. Keep
logical intent IDs stable so schema approval and cancellation remain effective.
This does not retroactively reverify completed migrations; existing mirrors
can be checked with manual verification and repaired through existing recovery.

Reject both inheritance parents and children during discovery and source
reinspection, including publication-time validation under the source DDL lock.
Use ONLY for publication creation and additions, including table recovery.

Drain eligible claims immediately with separate bounded queues: eight pipeline
reconciliations, two connection checks and two setup steps. Empty queues wait one
second. Continue to use catalog SKIP LOCKED claims, 60-second leases, 20-second
operation timeouts, revision fences and destination advisory locks. A fatal
catalog error cancels all queues and joins them before process restart. All
concurrency and polling limits live in pkg/api/limits.go. This removes the fixed
one-job-per-second ceiling without raising database or service memory budgets.
Endpoint saturation and expensive concurrent verification still require staging
load qualification; the pilot admission cap is not a worst-case latency promise.

Seal rotated source credentials against canonical account and connection IDs
read from the catalog. Equivalent accepted UUID spellings must not change the
encryption namespace used by the worker.

## Validation and scope

PostgreSQL regressions cover SQL/JSON nulls, exact json values, inheritance
rejection, late descendants, typed backfill and equivalent UUID forms. Native
replication tests cover approved backfill with a JSON-null primary key and
manual verification after null corruption. Scheduler tests cover a 300-pipeline
backlog with blocked jobs, queue independence, concurrency bounds, idle polling,
conflict handling, fatal errors and cancellation. The native acceptance suite
and race detector remain required. Production admission remains disabled.
