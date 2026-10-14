# ADR-1276: Diagnose and repair manual initial recovery evidence

Date: 2026-10-13

## Context

ADR-1275 observes manually approved preview-mode initial recoveries, but missing
saved execution proof leaves the review unresolved. The existing diagnosis and
repair endpoints only accept automatic-initial submissions.

## Decision

Extend the same endpoints and `failure-recovery-evidence` CLI command to submitted
preview-mode initial reviews with frozen expected generation zero. Inspect the
exact native job, singleton item, run, generation-one admission and terminal
completion timestamp. Repair remains first-result-wins: missing jobs, unverified
admission/completion, live runs, superseded generations and conflicting saved
results cannot be repaired. Account/app scope remains enforced. Confirmed manual
`result` and automatic `automatic_result` both return `already_confirmed`.

The existing actor, request ID and mandatory reason receipt and durable
`initial_evidence_repaired` event are committed atomically with the missing
ledger entry. Identical retries return the receipt, including after completion
confirmation; changed requests conflict. Reads never wake workers or append
history. Unsupported preview reviews report `not_submitted_manual_initial`.

Successful manual repair wakes its per-review completion observer, independently
of current policy mode, version, enabled state or pause. It does not wake
automatic policy execution. Only the observer subsequently confirms completion
and appends `initial_recovery_result`. Repair neither submits another job nor
charges/refunds automatic budgets, alters failure counters, releases an execution
slot directly or resumes a policy. Existing automatic and retry evidence repair
behavior is preserved. No API shape or database schema changes are required.

## Consequences

Operators can resolve a missing ledger result for an approved manual initial
recovery when its exact terminal proof remains retained. This does not reconstruct
pruned jobs, missing admission records or an earlier superseded generation.
