# Run task pipelines and bounded retries

Status: accepted for default-off preview implementation; release qualification pending.

## Context

Run has an encrypted managed DAG queue, immutable task versions, stored files,
and atomic customer/task budget admission. Agents need to combine those primitives
without sending source on every invocation or holding a client connection open.
Admission replay alone does not retry an execution that reached a failed terminal
state.

## Decision

Extend `POST /v2/execution-workflows` with named task steps, bounded batch expansion,
and explicit retry policies. Compile active task versions into the encrypted plan
and retain their catalog IDs and digests in non-secret continuation metadata.
Submission replay uses a digest of the normalized invocation, before consulting
current feature gates or retired task definitions. Worker authority is captured
from the original authenticated caller; a synthetic account principal is never
interpreted as a privilege grant.

Each physical attempt is a new ordinary Run. Its deterministic receipt label
identifies the plan, step index and attempt number. Existing principal-scoped Run
uniqueness recovers admission after a lost acknowledgement. Attempt admission checks
the live pipeline lease, cancellation flag, absolute deadline, previous terminal
receipt, retry classification and backoff in the same account-locked transaction
as task validation, artifact capabilities, payload insertion and budget reservation.
The previous receipt must belong to the same account, principal, workflow and step.
No second billing ledger is introduced. Every actual attempt reserves and settles
through the existing Run ledger; capacity polling consumes no attempt.

Multiple attempts require `safe_to_repeat=true`. The only eligible scheduler
failure codes are `restore_failed`, `execution_transport_failed`, and
`lease_expired`. Guest failures, protocol errors, OOM, timeouts, cancellation and
result contract failures do not retry. The VM backend maps guest-provided errors
to guest classifications before scheduler completion. At most three attempts per
step use bounded exponential backoff. An overall deadline includes queueing and
backoff; each admitted Run's immutable deadline is clipped to it.

A sidecar table reuses managed job receipts and lease ownership. Enhanced jobs keep
`scheduled_for` at a finite year-9999 sentinel. N-1 workers therefore do not claim
them, while N-1 readers can still decode the timestamp. A separate sqlc claim uses
the sidecar's due time. A transaction-local writer protocol and database trigger
reject N-1 Run writers using reserved pipeline labels. Rollback pauses new
admissions while current Run execution, reads, cancellation and deadline handling
remain possible in the new control-plane binary. Do not roll back every capable
worker until active pipelines have drained or been cancelled.

## Consequences

The first version is bounded to 16 expanded steps, eight requested parallel steps,
and a 24-hour maximum deadline (one hour by default). Existing account concurrency
and customer/task budgets may reduce available parallelism. Bindings, budgets and
live task availability are rechecked on every admission. Retirement blocks future
attempts; already admitted Runs retain their normal completion path.

Stored artifacts pass by owned file ID below `context.input_dir`. Inline artifacts
are copied into the next disposable bundle. Neither mode shares a guest filesystem.
Terminal plans erase ciphertext; remaining metadata contains labels, pins and
receipt lineage only. Cancellation is reported using the existing failed managed
status plus `cancel_requested` and a safe detail string, preserving old enums.

This does not guarantee exactly-once external effects: a lost transport can occur
after guest code has acted. Callers must use destination idempotency where needed.
Pipeline delivery to an agent remains the existing per-task Run completion path;
there is no separate whole-pipeline webhook contract in this increment.
