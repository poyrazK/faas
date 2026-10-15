# ADR-1154: Bounded bulk lineage retries

Status: Accepted
Date: 2026-10-13
Related: ADR-1151, ADR-1152, ADR-1153

## Decision

Expose a read-only preview of retained unresolved/conflicting lineage cases with
an optional exact reason filter and existing ascending outbox pagination. Preview
is a live read, not a reservation. Submit an explicit fixed selection of at most
200 unique outbox identities, expected revisions and original acceptance timestamps,
with a nonzero request UUID. Validate all selections under coverage/case locks
before recording the job. Repeating a retained request UUID with the same normalized
selection returns its current progress; another selection fails with 409.

Persist one payload-free operational job row per subscription/request UUID, with
bounded JSON selection, progress and recorded case results. The scheduler processes
at most 100 cases per retention sweep, one case per transaction. Lock the next job
with SKIP LOCKED, then coverage and case in the existing retry order. Evaluate with
the same authoritative receipt, content, owner and known root-incarnation checks as
individual retries. Derive a stable private retry UUID from subscription/job/outbox
identity. Commit root association, immutable individual result and job progress in
one transaction. Failed transactions roll back all three; workers need no leases.
Order pending jobs by last update for fair rotation between successfully checkpointed
cases. A database error aborts this sweep and is retried at the next normal tick.

Changed revisions or acceptance identity produce a changed item. Missing/expired
cases or removed subscriptions produce an unavailable item. Other cases report the
repair outcome, including unresolved or conflict, and their recorded result. These
expected failures do not abort the remaining fixed selection. Completed means every
item has an outcome, not that every root was repaired. Persist only identifiers,
timestamps and code-based diagnostics, never event payloads or handler errors.

Retain jobs for 30 days from creation, with bounded expiry pruning independent of
case/receipt retention. GET and workers ignore expired jobs. Submission rejects an
expired identity still awaiting cleanup; after pruning its UUID can be reused, so
clients must use new UUIDs and refreshed case guards after expiry. The original
acceptance timestamp prevents a replaced receipt/case incarnation from inheriting
an older job's authority. Hard app deletion cascades through coverage. New jobs are
customer intent written by apid; schedd only advances operational progress.

## Consequences

Operators can review and recover a bounded backlog without one client call per
case, and safely resume after process or response loss. A case may change between
preview, submission and scheduler evaluation; the guards fail closed. Multiple jobs
selecting one revision may leave later jobs changed. Jobs do not move the forward
scan cursor, change cumulative scan counters, execute handlers, backfill past outcomes,
clear losses, or reset an established coverage boundary. Successful repairs can
allow normal reconciliation to establish a future boundary. Existing aggregate-only
scans still have no case records. No cancellation, automatic reason-based retry
policy or historical case backfill is introduced by this decision.
