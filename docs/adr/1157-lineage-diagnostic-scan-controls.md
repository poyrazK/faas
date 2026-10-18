# ADR-1157: Lineage diagnostic scan controls

Status: Accepted
Date: 2026-10-13
Related: ADR-1154, ADR-1155, ADR-1156

## Decision

Add POST pause, resume and cancel subresources for the latest retained lineage
case-backfill scan. Require deploy/write scope and MFA. The path carries the
reviewed request UUID and the body carries the exact original `created_at` as
`expected_created_at`. Validate app/subscription ownership, then lock the existing
coverage row, the same lock held while the scheduler inserts diagnostics and
checkpoints progress. Check current UUID and creation timestamp after acquiring
that lock. Superseded/mismatched incarnations return 409 without changing state;
missing scan/subscription/coverage returns 404. No new table or column is needed.

Pause changes pending or waiting_for_retry_jobs to paused. Repeated pause on a
paused scan is a no-op. Paused is active for replacement guards and deadline
checks, but not runnable: both stores' worker selectors exclude it. Resume changes
paused to pending without resetting its cursor, counters, fixed upper receipt ID
or original 30-day deadline. Repeated resume on pending/waiting is a no-op. The
next sweep still checks active bulk retry jobs before observing receipts.

Cancel changes any live pending, waiting or paused scan to cancelled and stops
future diagnostic batches after commit. Existing case records, diagnostic IDs,
case revisions/retention, range and scan counters remain unchanged. Repeated cancel
on cancelled, completed or expired returns the current terminal state unchanged.
Pause/resume of completed, cancelled or expired returns 409. Expiry remains visible
immediately even while paused; pausing does not extend the work deadline.
A cancelled scan cannot resume, and repeating its original creation request returns
its current status without restarting it. A deliberate replacement requires a new
request UUID and the cancelled request UUID as the previous-request guard.

A scheduler batch already holding coverage can commit its diagnostics/checkpoint
before a control acquires the lock. Once pause/cancel commits, no further batch
for that scan is selected. Memory controls copy metadata and commit under the
existing mutex; PostgreSQL metadata changes share the coverage transaction.
Changed controls update only status and updated_at. Idempotent no-ops preserve
updated_at too. No control changes normal repair progress, collection boundaries,
execution outcome evidence or history-loss markers.

These are desired-state operations, not an immutable command-result ledger. Opposing
controls serialize in server lock order; a delayed retry after an intervening
opposite control can reapply its desired state. Read current status after a timeout
or conflicting operator action before repeating. Cancellation is terminal, so later
resume cannot undo it. A creation-time guard fences replacement, not control version.

## Consequences

Operators can pace diagnostic work and stop creating new cases without losing
already-recorded evidence. Source receipts/membership remain live and may expire
while paused; resume does not reconstruct missing history or extend case retention.
Normal repair, delivery attempts, individual retries and bulk jobs remain independent.
Controls do not execute customer code, repair lineage, revoke prior repairs or
cancel consumer deliveries. API, CLI and public SDKs expose the same lifecycle.
