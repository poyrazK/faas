# ADR-945: Structured recovery monitoring outcomes

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-941, ADR-942, ADR-944

## Context

Recovery status complete describes observation coverage, not monitoring
termination or delivery success. Automation needs a structured outcome in
addition to the process exit code and diagnostic stderr.

## Decision

Add an optional monitoring object to watch results and attached SDK error
results. Plain status reads omit it. It records stop_reason, passes,
retries_consumed, elapsed_ms and acknowledged_executions_terminal. Export
matching Go and Node types; Python uses its existing dictionary result.

Progress snapshots carry running. Final reasons are settled, timeout,
cancelled, read_failure, retry_exhausted, validation_failure and
callback_failure (Node/Python), plus checkpoint_failure for CLI persistence
errors. Authentication/authorization failures are read_failure and their
original errors remain available. Invalid SDK options rejected before
monitoring starts retain ordinary validation errors; Go can return a zero-pass
validation summary. No error details or credentials are copied into the summary.

Passes counts started bounded status calls, including failed validation/read
calls. Retries_consumed counts retry decisions charged to the existing total
budget, including a retry cancelled during its wait. Zero-budget transient
failures remain read_failure rather than retry_exhausted. Elapsed time is
nonnegative milliseconds measured with the runtime's monotonic clock, through
the watch return or failure. SDK callback time is included. CLI final saving
and output happen after the watch; checkpoint_failure overrides the reason
without changing counters or the measured watch elapsed time.

Acknowledged_executions_terminal uses the existing conservative settled
predicate: complete coverage and matching terminal evidence for every queued
attempt, with unknown/read-error attempts preventing settlement. Rejected,
skipped and not_attempted attempts have no acknowledged execution to monitor.
ADR-946 optionally adds a freshness requirement and corresponding counts.
The predicate can be true when no queued attempt exists; these outcomes stay
explicit in rows. Terminal failures can settle monitoring but are not delivery
success. A timeout/callback/checkpoint failure can coexist with terminal
observations; the stop reason describes the call's outcome.

Preserve the same summary in final stdout JSON and successful final checkpoint
saves. In-progress checkpoints have running, and an unsuccessful checkpoint
save cannot reliably record its own failure: consult final stdout. Stop reasons
do not replace exit codes or attached SDK errors. Existing complete, statuses,
counts, attribution and original attempts retain their meanings.

Resuming starts new counters and elapsed time for the new watch invocation.
Saved monitoring aggregates are historical metadata and do not control
selection validation, retry budgets or terminal decisions. Plain resume or
refresh omits monitoring; the watch regenerates it from current execution.

## Consequences

Automation can distinguish watch settlement, timeout and failure directly from
JSON. Unknown replay outcomes remain unknown, and no recovery is dispatched.
Local metadata is editable evidence, not signed proof. Production outcome and
interruption qualification remains pending. No local tests are added or run;
compliance registries are unchanged.
