# ADR-1167: Retained lineage retry-job health

Status: Accepted
Date: 2026-10-13
Related: ADR-1154, ADR-1160, ADR-1163

## Decision

Attach optional lineage_retry_jobs diagnostics to consumer execution health and
therefore the execution portion of combined delivery health. Observe currently
retained pending/paused jobs in the existing account/app/subscription scope and
read transaction or memory mutex, independently of the requested outcome window.
Do not include completed, cancelled, expired or future-created jobs, case
selections/results, event payloads or command receipts.

Read at most 501 metadata rows to observe the earliest 500 retention deadlines and
detect truncation. Order by retain_until, updated_at and request UUID. Keep the
memory store's selection bounded to the same 501 rows while scanning its scoped
map. When counts_complete is false, counts are lower bounds and timestamp extrema
cover only those observations. Return up to ten samples, prioritizing retention
risk then stalled pending work within the observed set. The sample is not a full
job listing and omissions cannot establish absence of a condition.

Report pending/paused counts and remaining cases, stalled pending, retention risk,
unknown progress, oldest observed pending activity and earliest observed expiry.
Pending jobs with no activity for 15 minutes receive an advisory stalled flag;
paused jobs never do. Active jobs within 24 hours of original expiry receive a
retention-risk flag, including paused jobs. These limits live in pkg/api/limits.go.
Inactivity is measured from the authoritative job update timestamp, which includes
state controls; resuming work resets inactivity. It is not proof of scheduler
failure, and it never triggers automatic retries, resume or cancellation.

Record optional last_progress_at in the existing job JSON snapshot after every
committed scheduler evaluation or skip, atomically with its processed cursor.
Controls preserve it. Copy the pointer when cloning memory jobs. Last progress age
is separate from activity age; before the first evaluation use creation as the
known progress baseline, with no last_progress_at timestamp. Legacy evaluated
snapshots without this field remain unknown rather than substituting control time.
New workers establish progress evidence on their next committed evaluation.
Upgrade all workers and API writers for consistent preservation; older writers
may omit the new JSON field during a rolling upgrade. Do not backfill guesses.

Add an append-only partial index migration on active jobs ordered by subscription
and retention/activity/UUID for the bounded read. No new table columns or clone
schema registry entries are needed. Index downgrade does not change job data.
Expose optional diagnostics and progress metadata in existing API models, SDKs,
and execution/delivery-health CLI output. Existing handler outcome completeness,
execution truncation and alert eligibility remain separate and unchanged.
