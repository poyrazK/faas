# ADR-728: Recovery execution outcomes

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Record the exact replay admitted by an execution recovery item and expose read-only execution observations beside admission progress.
- **Why:** A completed recovery job proves that selected work was queued or skipped; it does not show whether the handlers recovered.

## Replay attribution

Execution-mode replay admission records `replay_invocation_id`,
`replay_generation`, and `replay_created_at` on the recovery item in the same
transaction as the replay. Plain/keyed replay children record their returned
identity, including generation zero. Unified in-place dead-letter replay records
the original invocation ID and incremented generation. The existing worker still
holds the job lock; the replay savepoint sets the item to `queued` with its
identity before the outer transaction updates pacing and completion. Errors
roll back both admission and tracking. Memory stores record the same identity
under their mutex.

The nullable columns have an all-or-none constraint and require a queued item.
They deliberately have no invocation foreign key: execution retention must not
remove recovery metadata. The migration does not reconstruct identities for
previously admitted items; those admissions are unknown. Pending items in older
jobs gain identity when processed after upgrade. The clone schema registry
continues to classify these columns as operational.

## Observations

Existing job reads return an optional `execution` summary for execution-mode
jobs. Existing item reads expose optional replay identity and `execution`
observations for queued execution-mode items. Preview, pending, skipped, and
cancelled-before-admission items do not claim execution observations. Routing
recovery still reports routing admissions without guessing which subsequent
handler generation belongs to that job.

First read a retained invocation owned by the job's account and app with the
recorded ID, creation time, and replay generation. Classify pending/no attempts
as queued, pending/attempts as retrying, dispatching as running, completed as
succeeded, and the remaining terminal states as failed, dead-lettered, expired,
cancelled, or superseded. An uncertain invocation outcome is unknown.

If the invocation is missing or has advanced to another generation, inspect the
latest retained attempt for that exact invocation ID/generation, scoped to the
same account/app and started no earlier than the recorded invocation creation.
Only a finished `succeeded`, `failed`, `dead_letter`, or `cancelled` attempt proves
a terminal outcome. Running, retry, uncertain/unknown, expired history, and
missing history report unknown. A previous attempt's terminal-looking record
cannot override a newer retry or running attempt. Attempt-derived observations
include their recorded attempt number and finish time. Expiry/supersession that
occurred without a terminal attempt cannot be reconstructed after row retention.

Items report state, observation time, attempts, optional completion time, and
source (`invocation`, `attempt_history`, or `unavailable`). Job summaries count
all admitted execution-mode items, including untracked legacy admissions as
unknown; counts sum to `tracked_count` and admission `queued_count`. These are
current retained observations, not permanent outcome certificates. Later replay
children never replace this job's results. Later in-place generations require
retained evidence of the tracked generation, rather than borrowing the newer
row's state. Retention can turn a previously known result into unknown.

## Consistency and lifecycle

PostgreSQL job and item GETs use read-only repeatable-read transactions. The
summary reads at most the existing 10,000-item job bound; item reads observe only
the selected page. SQL uses the invocation primary key and the existing attempt
identity index; no extra polling worker or projection is introduced. The memory
store reads invocations and retained attempts under its existing mutex and
copies response pointers. Reads do not admit, retry, or mutate execution state.

Job state, admission counts, cancellation, pacing, quota, job expiry, and
metadata retention keep their existing meanings. A cancelled or expired recovery
job can still show admitted handlers running or completing. Cancelling a job
does not cancel handlers already admitted. A `completed` job with failed
executions is still admission-complete; operators inspect the execution summary
before deciding on another recovery. Success describes the recorded handler
completion, not exactly-once application side effects.

The API, Go client, generated Node/Python clients, CLI status and item output,
and documentation expose the observations through existing authenticated routes
and scopes. No new endpoint or write permission is required.
