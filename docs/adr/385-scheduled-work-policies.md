# ADR-385 · Durable scheduled work policies

- **Status:** accepted for recurring Jobs and deployment-command Crons
- **Date:** 2026-09-30
- **Decision:** Persist an immutable schedule-occurrence decision for each
  nominal scheduled time. Snapshot the policy and schedule revision with the
  occurrence. Use a cursor compare-and-set in the same store transaction that
  records skips or admits the execution.
- **Why:** Cron-driven work needs overlap, lateness, and recovery semantics at
  the scheduler boundary. Per-partition retries also need explicit business
  outcome classification and durable evidence so callers do not rebuild
  bookkeeping around each job.
- **Consequences:** Scheduled Jobs and deployment-command Crons gain a
  versioned policy, durable occurrence history, classified attempt decisions,
  and API/CLI inspection. PostgreSQL migrations and MemStore behavior must stay
  aligned. External side effects still require idempotency when completion is
  uncertain.
- **Rejected alternatives:** Leave locking, missed-run handling, and retry
  classification to each application; or infer retry safety from generic
  transport/guest failures. Both options duplicate state machines and can
  repeat side effects without confirmed evidence.

## Policy contract

`SchedulePolicy` version 1 contains overlap (`allow`, `skip`, or `replace`), an
optional first-start deadline, and missed-run recovery (`skip` or
`coalesce_latest`). Overlap is scoped to one Job or Cron definition. A deadline
is inclusive: work may start exactly at the deadline, and a late queued task is
settled as `missed_deadline`. Once one Job partition has started, the
occurrence's first-start deadline no longer blocks its remaining partitions.

`skip` records a due occurrence without admitting work when an earlier
scheduled execution remains active. `replace` preserves the due cursor while
it requests cancellation, then admits the occurrence only when the old
execution is confirmed stopped. `coalesce_latest` stores older recovered due
times as `coalesced` and considers the newest due time. `skip` records older
recovered due times as missed. Each occurrence retains a reason, blocker where
known, and policy snapshot so Operations can distinguish queued, skipped,
coalesced, late, and terminal executions after the schedule changes.

Recurring Jobs create a durable one-task run for each admitted occurrence.
Their existing indexed tasks retain stable partition identity and attempt
history; a run retry or `replay-failed` selects only the failed partitions.
Deployment-command Crons create one command task per admitted occurrence and
use the app's current live deployment. Their failure rules classify confirmed
exit codes or structured application outcome codes, and configured retry
limits apply to that task. Guest side effects still require idempotency: a
missing completion receipt cannot prove whether the external operation
committed.

HTTP request Crons keep their existing synthetic-request dispatch contract and
do not accept the new schedule or failure policy objects. A future change must
atomically connect their occurrence record to the queued invocation and apply
the start deadline while the invocation is still pending before enabling the
same policy surface for them.

## Storage and interfaces

Job and command-Cron occurrences use a schedule-revision plus nominal-time
identity, protected by unique indexes. A row-lock transaction verifies the
observed cursor and revision, evaluates deadline/overlap, advances the cursor,
and creates the run or task. Postgres status triggers and MemStore lifecycle
helpers keep the occurrence projection in sync. `GET /v1/jobs/{name}/occurrences`
and `GET /v1/crons/{id}/occurrences` expose account-scoped cursor pages; the
Gregale CLI mirrors both endpoints.

`FailureRules` are an explicit versioned mapping from guest exit codes or
structured application outcome codes to `retry` or `fail_partition`. Job and
command-Cron guests report structured codes in the bounded result manifest;
a mapped code can classify a zero exit. Unmatched failures use the declared policy.
Infrastructure failures remain retryable. A configured `uncertain_outcome`
decides whether a missing completion receipt is held for reconciliation or
retried; retry must be explicit because it can repeat guest side effects.
Definitions without `FailureRules` retain their existing retry behavior. No
generic HTTP or guest error is treated as proof of business retry-safety. HTTP
status matchers remain unavailable on the scheduled-work execution path. The
per-partition attempt journal and linked replay continue to preserve confirmed
outcomes.

## Rollout

Apply the append-only migration before deploying API, scheduler, or worker
code. Keep schedule policy and failure-rule JSON on the API and CLI surfaces,
with occurrence history available to Operations. Validate schema parity,
Postgres and MemStore behavior, overlap cancellation, deadline enforcement,
recovery/coalescing, per-partition classification, and replay of only failed
partitions before rollout. Do not describe the execution guarantee as exactly
once; customers still need idempotent external effects and Gregale retains an
explicit uncertain outcome when a receipt is missing.
