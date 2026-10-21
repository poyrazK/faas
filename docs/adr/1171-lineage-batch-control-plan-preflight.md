# ADR-1171: Lineage batch-control plan preflight

Status: Accepted
Date: 2026-10-13
Related: ADR-1161, ADR-1162, ADR-1170

## Decision

Add a read-only CLI preflight for the same strictly validated fixed plan used by
batch-control apply. Require explicit app/subscription targets, reject a different
subscription locally, and resolve the app to the saved stable UUID before reading
jobs. Never POST, change a plan, allocate command UUIDs, or refresh its selection.

Read each current job and its complete bounded unfiltered receipt journal using
existing APIs. Verify scope, original creation/retention, job progress metadata,
receipt sequence, uniqueness, and immutable receipt metadata. Reads are separate
observations, not a transaction spanning job state and control history.

Recognize an existing matching command receipt before checking new-command state
or receipt capacity. Report already_acknowledged for matching actions and
command_conflict for a UUID previously used for another action. New commands report
ready, no_op, terminal, or receipt_limit according to existing control semantics.
No-op new commands still consume a receipt slot. Mismatched creation or retention
reports stale_guard; locally observed expiration reports expired. Failed reads or
invalid replies remain unverified with their error; do not infer readiness from
missing evidence. Continue independent entries after failures.

JSON reports include advisory, complete, ready and per-job results. Complete means
all entries were assessed; definite blockers can be complete. Ready means every
entry is ready, no_op or already_acknowledged. Exit 0 only when ready, 1 for any
blocker or unverified entry, and 130 for interruption after emitting partial results.
Per-request and total deadlines bound reads. No results file is written.

Readiness is advisory: job state and receipts can change between reads or before
apply, and retention can expire. Preflight does not reserve receipt slots, prove
write-scope/MFA admission, or replace server guards. Reuse the unchanged plan and
command UUIDs for apply. No API, SDK, schema or compliance registry changes.
