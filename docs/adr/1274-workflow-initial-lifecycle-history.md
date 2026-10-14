# ADR-1274: Initial recovery lifecycle in shared history

## Status

Accepted, 2026-10-13.

## Context

Shared history preserves initial evidence repairs and attention transitions, but
preparation, submission, cancellation, expiry, staleness and automatic final
results remain only on retained individual reviews.

## Decision

Append `initial_recovery_prepared`, `initial_recovery_submitted`,
`initial_recovery_cancelled`, `initial_recovery_expired`,
`initial_recovery_stale` and `initial_recovery_result` prospectively to the existing
durable history ledger. Capture preparation only on new review insertion; capture
decisions only after a real retained transition, and results only when the existing
automatic worker commits its verified final outcome. Early-return replay branches
append nothing. Reads and advisory freshness observations cannot create events.

Use the retained transition actor, reason and occurrence time, with frozen policy
version, initial failure, review, run, target resumed generation, and available
job/diagnosis links. Result events use the recorded outcome and outcome reason;
submission and alert clearance never imply successful completion. Preparation's
generation is a target until admission, not execution proof.

Write history under the memory store lock or in the same PostgreSQL transaction
as review insertion/update, submission or final-result capture. Preserve account
scope, filters, sequence-fenced pagination, retention and event cap. History rows
survive source review/job pruning within these bounds. Old review events are not
backfilled or relabeled durable. Preparations and decisions apply to both preview
and automatic-initial reviews; final results follow the existing automatic worker
and do not introduce a new manual-job result observer.

An append-only migration expands history event kinds and validates lifecycle
associations, actors and terminal-result outcomes, preserving all existing
candidate, retry and initial-evidence checks. Replay preserves retained history;
downgrade refuses while lifecycle events remain. Regenerate the schema snapshot
and verify SQLC against it. No API route, new quota or scheduler authority is added.
Existing CLI/API/SDK history models already accept these string kinds and fields.

## Consequences

The shared timeline connects an initial failure to preparation, its audited
decision, submitted job and recorded automatic result without opening every
review. Reading never submits work or spends a budget. History is durable within
existing retention, while missing outcome proof remains unresolved.
