# ADR-20261114143410: Structured recovery for CLI Job waits

Status: Accepted
Date: 2026-11-14
Related: ADR-20261113183052 (async invocation waiting),
ADR-20261113145429 (request-ID diagnostics)

## Decision

For plain `job wait` and `job run --wait`, a local wait failure in JSON mode
adds one Problem diagnostic on stderr. The existing stdout receipt keeps its
fields, accepted/last-known run snapshot and optional raw-error field. Sensitive
text in that legacy error and its resume command is redacted. Successful waits
and terminal failed/cancelled/dead-letter runs keep their current output.

The Problem carries the cause code, status, exit category, actual exit code,
validated request correlation when supplied by the failing API response, and
`recovery` with `job`, `run_id`, `inspect_command` and `resume_command`.
Invocation and Job waits share the envelope writer and correlation handling.

Recovery uses the selected profile and existing POSIX quoting. Inspection calls
`job status`; resumption calls `job wait` with the original local timeout and
polling interval. Both name the exact run UUID. A `latest` selector is resolved
once before waiting and is never copied into recovery commands. No command is
executed automatically. Missing/unverified acceptance identity does not gain a
fabricated run target. A standalone target does not prove that the run exists
or is still running. Connection/credential environment overrides must still be
retained when the user resumes.

Preserve the Job wait exit contract: timeout is 124, interruption is 130,
polling errors are 1, success is 0, and unsuccessful terminal runs are 1.
`job_wait_timeout` distinguishes the overall local deadline from an individual
HTTP-request timeout. `job_wait_interrupted` identifies stopped local polling.
API failure codes/statuses and remediation are preserved; transport and local
protocol failures use the existing shared classifications. The category follows
the existing process exit contract, including exit 1 for polling refusals.

The new diagnostic is emitted after writing the stdout receipt successfully,
so an output-write failure keeps its existing diagnostic priority. Human output,
condition waits and artifact collection retain their current
contracts. Job submission, server retry policies and cancellation are unchanged.

### Image-readiness waits

Apply the same stderr envelope to `job get --wait-ready` and the preparation
phase of `job run --wait-ready`. Preserve their stdout receipt fields and exit
codes. The local codes are `job_image_wait_timeout`,
`job_image_wait_interrupted`, `job_image_preparation_failed` and
`job_image_changed`; API causes retain their code, status and correlation.
Recovery records `phase: "image_wait"`, `submitted: false`, the Job and
read-only inspection. Only a verified selection supplies Job/image identity;
a pending selection supplies a read-only `job get --wait-ready` resume command
with the selected profile and timing options. It never submits a deferred run.

A response for a different Job, account, image reference or resolved digest
cannot replace the last verified snapshot. Changed or failed selections omit
automatic resumption. Resuming starts a fresh readiness check of the named Job;
recorded image metadata must be compared with the current selection. Existing
commands do not pin an image across CLI invocations. Unverified selection never
gains a fabricated identity. Error text and recovery fields apply the shared
credential redaction rules; stdout schema and write-failure priority remain.

## Verification

Public-dispatcher tests cover accepted/standalone waits, local deadlines,
authentication/server failures, transport and malformed/mismatched responses,
last-snapshot preservation, missing receipts, pinned latest selection, request
correlation omission/redaction and terminal outcomes. Real subprocess SIGINT
tests require exit 130, closed polling reads and unchanged submission counts.
A built-CLI journey executes the emitted inspection/resume commands after a
timeout, proving they retain the original run when latest has advanced and
never submit or cancel work. Existing invocation recovery regressions protect
the shared writer's wire shape and correlation fields.

Image-readiness tests cover the same failure classes, learned/changed digests,
identity and account changes, zero run submissions on every preparation failure,
and real SIGINT cancellation. A built-CLI journey executes both emitted commands
after preparation succeeds and requires that no run is submitted, including
after stopping a pre-submission wait.
