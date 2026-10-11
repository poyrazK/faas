# ADR-923: Wait for recovery notification retries

Date: 2026-10-09
Status: Accepted

## Context

Retry history exposes the outcome of each originally queued delivery generation. Operators and scripts need a bounded way to observe a saved request through completion, while retaining its identity and latest evidence if the wait stops.

## Decision

Add `--wait` and `--timeout` to `gregale events recovery-notification-retry-history`. Waiting requires `--request-id`; an explicit timeout requires waiting and must be positive. The default deadline is five minutes. Poll the existing read-only request detail endpoint immediately, then five seconds after each pending response. Each read is bounded by the existing five-second recovery request timeout and the remaining overall deadline. Interrupt cancels both reads and timers.

Pin the returned app, request/job identity, original decision time, receiver targets, queued/skipped decisions, reasons, and originally queued generations. Reject changed or malformed observations, including duplicate targets and inconsistent queued generation increments. Latest delivery generations may change independently. Track outcomes for the original queued generations only. Success requires at least one queued target and every queued target succeeded. Stop immediately on any known failure or unknown evidence; other receivers may still be pending. Skipped receivers remain separate and do not establish delivery success. All-skipped requests are inconclusive.

Print progress counts to stderr only when they change. Text output includes the final status, original request identity, and the last valid per receiver detail. JSON output emits one final receipt containing `job_id`, `request_id`, `status`, optional `reason`, counts, and optional `last_observation`. Preserve the last valid observation on timeout, interruption, or later read failure. If the first read fails, identity and a failure status remain available without an observation. Transport/authentication errors and pruned jobs stop the wait; no automatic retry request is issued.

Exit codes: 0 succeeded; 1 known receiver failure, read/protocol error, or invalid arguments; 2 inconclusive; 3 wait deadline exceeded; 130 interrupted. Timeout or interrupt does not cancel server delivery. Existing one-shot JSON and text history remain available without waiting.

## Consequences

Scripts can distinguish completion, failed delivery, missing evidence, and timeout while keeping the request identity for later inspection. This is a CLI polling feature using the existing Go SDK method. No API route, SDK wire model, migration, storage, retention schedule, or dispatcher change is required.
