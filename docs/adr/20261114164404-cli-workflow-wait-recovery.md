# ADR-20261114164404: Structured recovery for CLI Workflow waits

Status: Accepted
Date: 2026-11-14
Related: spec UX §3.2; ADR-20261113183052 (async invocation waiting)

## Decision

JSON `workflow wait` and the wait phase of `workflow run`, `workflow resume`
and `workflow retry` use the shared Problem envelope with optional `recovery`:
`run_id`, `inspect_command` and `resume_command`. Inspection uses `workflow
status`; continuing a wait uses `workflow wait`. Both name the validated run
UUID and selected profile with existing POSIX quoting. The latter also keeps
the original timeout and polling interval; earlier hints lost the interval.
Never replay the original mutation, input, step arguments or idempotency key
to continue waiting. A resolved `latest` is not resolved again in recovery.

Keep the Workflow run DTO on stdout, without adding a wrapper. Accepted waits
retain acceptance or their last verified observation. A standalone wait that
observed the requested run retains that snapshot on a later permanent read
failure; previously it discarded it. No successful observation means no
fabricated stdout snapshot. A response identifying another run cannot replace
the selected snapshot. An unverified acceptance gets no fabricated recovery
ID, and is validated before attempting to journal it so journal warnings do
not corrupt JSON stderr. A snapshot-write failure keeps diagnostic priority.

Retain polling policy and exit contracts. Permanent API refusals keep server
codes, status, remediation, validated request IDs and the existing HTTP-to-exit
mapping. HTTP 408/429, server errors, transport failures and malformed reads
continue polling under the existing deadline. Overall deadline expiry exits
124 with `workflow_wait_timeout`; local interruption exits 130 with
`workflow_wait_interrupted`. Local outcomes carry no invented or stale API
correlation. Successful, failed, cancelled, dead and unknown terminal outcomes
keep their existing stdout DTO and exit behavior. Recovery is additive to
existing terminal error Problems; unsuccessful terminal outcomes still exit 1.

The common writer is also used by invocation waits without changing their
wire shape. Apply existing credential redaction to Problem and recovery
metadata. Human output keeps its existing behavior with the corrected resume
command. Connection and credential overrides must be retained by the caller;
a recovery target alone does not prove acceptance or current run state.

## Verification

Public-dispatcher tests cover all four entry paths, exact API exit mapping,
correlation/redaction, last-snapshot preservation, mismatched identity, resolved
latest selection, timeout, terminal outcomes, invalid acceptance and unchanged
transient polling. Real subprocess SIGINT tests require exit 130, closed reads
and exactly the original mutation count. A built-CLI journey executes both
emitted commands after each wait times out, requiring only reads of the same
UUID and no repeat start, resume, retry or cancellation.
