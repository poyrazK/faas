# ADR-1155: Cancellable bulk lineage retries

Status: Accepted
Date: 2026-10-13
Related: ADR-1153, ADR-1154

## Decision

Add POST on a retained bulk lineage retry job's `/cancel` subresource, requiring
existing deploy/write scope and MFA. Require the exact original `created_at` in
`expected_created_at` to fence a replacement after expiry and request-UUID reuse.
Validate tenant/app/subscription ownership, then lock the job row with FOR UPDATE,
the same first lock held by scheduler evaluations. Cancellation takes no coverage
or case lock and introduces no inverse dependency on the worker's lock order.

A worker already holding the job lock may finish and commit its evaluation before
cancellation acquires that lock. Once cancellation commits, workers selecting only
pending jobs cannot start another evaluation for that job. In one transaction,
change pending job state to cancelled and mark all remaining items cancelled with
reason `job_cancelled`. Preserve fixed selection, evaluated results, processed
count and original retention deadline. Processed counts evaluated/skipped cases;
items cancelled without evaluation do not increment it or change case revisions.
The memory store uses its existing mutex and copies the job before mutation.

Repeated cancellations with the same job UUID and creation timestamp return the
unchanged terminal job. A job already completed remains completed. A changed
creation guard returns 409 with `event_lineage_retry_job_changed`, including for
terminal jobs. Missing/expired jobs or removed subscription access return 404.
Check expiry again after a lock wait. A timeout may leave the outcome uncertain;
repeat the same UUID and guard to resolve it. Transaction failure rolls back the
cancellation. Repeating original job submission returns cancelled progress with
200 and never resumes it; a deliberate new job needs a new UUID and reviewed guards.

Extend the existing state CHECK to accept cancelled through an append-only
migration. No new table, column, quota, daemon or clone classification is needed.
New job creation timestamps use PostgreSQL-compatible microsecond precision so
the initial response and later reads identify the same job incarnation. For older
jobs use the original timestamp from GET. Cancellation does not extend retention.
The downgrade's original CHECK deliberately fails if cancelled rows remain;
physically prune them first instead of resuming work or falsifying its outcome.

## Consequences

Operators can stop unprocessed recovery work and inspect exactly which results
committed before cancellation. Existing repairs remain applied. Cancellation
neither interrupts customer code nor cancels individual delivery attempts, and it
does not revoke an independently submitted retry or another bulk job. It never
resets scan cursors, coverage boundaries, scan counters or history-loss markers.
The guarantee starts at committed cancellation, not when a request is sent.
