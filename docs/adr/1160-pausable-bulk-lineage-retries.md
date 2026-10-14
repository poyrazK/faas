# ADR-1160: Pausable bulk lineage retries

Status: Accepted
Date: 2026-10-13
Related: ADR-1154, ADR-1155, ADR-1156, ADR-1158

## Decision

Add guarded POST pause/resume subresources for retained bulk lineage retry jobs,
using deploy/write scope, MFA, no-store and the existing control timeout. Require
the reviewed original created_at to fence the job incarnation. Validate account,
app and subscription ownership, then acquire the same job row lock held first by
the scheduler. Controls do not acquire coverage before the job or change worker
lock order. Recheck retention after lock waits; absent/expired targets return 404.

Pause changes pending to paused; resume changes paused to pending. Repeating the
currently desired state is a no-op preserving UpdatedAt. Guard mismatches and
pause/resume on completed/cancelled jobs return 409 with
`event_lineage_retry_job_changed`. Preserve fixed selections, immutable evaluated
results, pending remaining items, processed count, original creation time and
retention deadline. Only state and update time change. PostgreSQL returns the
saved database update timestamp; the memory store clones before mutation and
commits only after successful serialization.

An evaluation already holding the job lock may commit before pause acquires it.
After pause commits the existing pending-only scheduler selectors exclude this
job. Resume rejoins the queue using its new update timestamp and starts at the
saved processed position. Workers still validate original per-case guards; other
repairs and source retention remain live during a pause. Creation with the same
UUID/selection returns a paused job with 200 and never resumes it. Extend guarded
cancellation to paused jobs, preserving its existing terminal no-op semantics.

Treat paused and pending jobs as unfinished work for diagnostic case-backfill
waiting. Update PostgreSQL and memory gates together. Pause therefore continues
to block that subscription's diagnostic scan until retry work resumes and finishes,
is cancelled, or expires. Diagnostic scan deadlines and normal forward lineage
repair remain independent. Expiry and pruning already apply to all job states.

Add paused to the existing state CHECK through an append-only migration. Downgrade
fails while paused rows remain; explicitly resume/cancel or expire/prune them
before removing support. Apply the migration and upgrade API/scheduler binaries
before using controls. No table, column, worker budget or retention extension is
introduced. Listing filters and public DTO/SDK status enums include paused. CLI
watching continues through paused state; preflight accepts a matching paused job
as already submitted, and remaining-case export still requires a terminal job.

## Consequences

Operators can temporarily stop queued reevaluations while retaining their reviewed
selection and completed work. This does not pause consumer deliveries, handlers,
individual retries or unrelated repair work. These are desired-state controls,
not immutable command receipts: conflicting actions follow job lock order and a
delayed repeated pause/resume can reapply after an opposite action. After a lost
response or timeout, read current state before deciding which action to repeat.
No outcome history, case revision, coverage boundary, loss marker or diagnostic
cursor is reconstructed or reset by a job control.
