# ADR-595: Workflow-backed customer Operations

## Status

Accepted — 2026-10-06. Customer admission remains disabled pending qualification.

## Context

HTTP customer Operations connect input, owner, progress, results and completion
delivery. A whole-handler retry cannot retain completed internal steps. Native
workflows already persist step inputs, outputs, attempts and resume generations.
Their HTTP actions normally use the current deployment; Operations retain code.

## Decision

An immutable Operations definition may name `workflow`. Its POST path is the
submission route, not an additional handler call. Resolve the named workflow
from the same immutable deployment. The first adapter accepts a linear chain of
HTTP actions in the default production scope, with progress stages matching the
steps in order, manual trigger,
no conditional branches, waits, iteration, outbound actions, compensation or
automatic retries. An omitted retry policy is captured as one attempt per
generation. Other workflow contracts remain unchanged.

Admission commits the tenant-owned operation, run, seeded steps, execution
association, idempotency receipt, code pins and initial event together. Both
Operations admission and workflow active-run quotas apply. The run retains its
input and definition snapshot. Dispatch uses the operation's private deployment
and release pins, checked against the current run/step/attempt and live lease;
frontend headers cannot grant this authority. Stage progress comes from durable
step transitions. The final workflow output is validated against the Operations
output schema. Completion delivery uses the independent existing outbox.

An interrupted HTTP action is closed as unknown, never automatically repeated.
The operation requires reconciliation. An account-authorized `safe_to_retry`
recovery with evidence applies the existing workflow resume eligibility plan in
the same transaction as the operation generation and recovery receipt. It keeps
the run ID, successful outputs, original input, snapshot and pins; failed actions
get monotonic attempts and the same action idempotency keys. It does not prove
that arbitrary HTTP effects are safe to repeat. Success/failure/cancellation
resolutions retain their existing operator confirmation contract. Direct run
resume or step retry must not bypass Operations recovery.

Cancellation fences the run and stops the scheduler's active HTTP context. A
dispatched action with an uncertain result requires reconciliation; stopping a
call never proves reversal of external effects. The HTTP Operations runtime
claim and artifact-upload helpers remain specific to their invocation contract.
Workflow outputs are typed JSON in this slice; workflow artifact attachment
needs its own execution authority before it can reuse private file delivery.

## Validation and rollout

Memory/PostgreSQL tests cover atomic admission, tenant isolation, duplicate
submissions, quota rollback, durable progress, interrupted attempts, explicit
resume, preserved outputs/pins, stale completion and independent delivery.
Scheduler acceptance must show a three-step chain resumes after step two fails
without executing step one twice. Apply the additive migration and update all
workflow workers and gateway dispatch before enabling a qualified preview.
No production configuration, deployment or customer admission is changed here.
