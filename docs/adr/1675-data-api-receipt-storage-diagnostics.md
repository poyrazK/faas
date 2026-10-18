# ADR-1675: Data API receipt storage diagnostics

Status: Accepted

## Context

V2 note creation, update/delete, atomic batch and generated transaction receipts
retain success snapshots until owner cleanup. Cleanup previews count eligible
deletions but omit the live population, backlog age and storage usage. Runtime
readiness cannot establish receipt storage health.

## Decision

Ship `receipts:diagnostics` in the note starter and generated v2 transaction
packages. Use one shared reporter copied into generated packages, fixed reviewed
contracts and the existing owner migration binding. The starter defaults to all
three v2 contracts and accepts a fixed receipt-kind selector. The transaction
report selects its generated receipt table. No schema discovery, arbitrary
identifier input, serving-role grants, HTTP endpoint or cleanup is added.

For each contract use a read-only READ COMMITTED transaction. Verify exact current
table ownership, regular non-inherited table shape, timestamptz expiry and absence
of FORCE RLS. Acquire ACCESS SHARE and recheck ownership/shape with a fresh
snapshot so concurrent DDL cannot change the relation afterward. Set search_path
to pg_catalog and row_security off to refuse silently filtered results. Aggregate
total, active and expired counts, oldest expired deadline and age against one
database statement timestamp and statement snapshot. Null expiry makes the
report unavailable. Never select request/response payload columns.

Report table bytes including TOAST, index bytes and total physical relation
bytes. These observations measure allocation, not live payload size or bytes
reclaimable by deletion. Physical sizes can change during a report. Each
contract has a separate snapshot and observation time.

Central timeouts are 5 seconds for connection and each statement, 1 second for
lock waits and a 6 second client query fallback. A single connection inspects
the fixed list sequentially, without retry or caching, then closes. Exact scans
can time out on large tables.

Output versioned JSON with per-contract ok/unavailable status and a top-level
ok/incomplete status. Counts, ages in milliseconds and sizes are decimal strings;
absent oldest deadline/age are null. Unavailable entries omit measurements and
expose fixed reasons only. Exit nonzero for incomplete/invalid invocations.
Never print credentials, driver errors, actors, tenants, keys or payloads.

## Consequences

Operators can monitor growth and backlog using explicit owner work. Protect
these cross-user aggregates as operator information. Expired counts indicate
deadline eligibility; cleanup may skip locks. Purging may leave physical
allocation unchanged until normal database maintenance. Existing expiry and
recovery guarantees remain intact; legacy v1 is outside this expiry report.

## Validation

Central limit parity and CLI materialization checks accompany real PostgreSQL
tests of note migrations and generated transaction SQL. Cover empty/populated
tables, cross-identity counts, exact ages and sizes, unchanged receipt contents,
hidden serving reads, non-owner refusal, missing/FORCE RLS tables, bounded DDL
contention, actual statement cancellation, read-only settings, sanitized CLI
failures and backlog reduction after protected owner cleanup.
