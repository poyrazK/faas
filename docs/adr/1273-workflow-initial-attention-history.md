# ADR-1273: Initial recovery evidence alert guidance and history

## Status

Accepted, 2026-10-13.

## Context

Initial recovery queue entries expose exact-generation evidence. Their alerts
still contain generic operator guidance, and shared history omits their repair
receipts and blocked/cleared attention transitions.

## Decision

Enrich initial attention captures with `evidence_kind: initial`, run, resumed
generation, diagnosis reason, verified repairability, diagnosis URL and the
`failure-recovery-evidence` inspection command. Reuse existing initial evidence
observation inside the worker transaction/lock. Recommend audited repair only
when the exact retained terminal outcome is verifiably repairable. Job pause and
quota alerts keep their operational guidance rather than suggesting proof repair.

Keep committed worker progress as clearance authority. Existing blocker reasons
and proven admission/execution/result transitions retain their semantics; a policy
change, dismissal or inspection read cannot clear attention. Repeated unchanged
reasons and diagnoses remain quiet. For evidence blockers, a changed detailed
diagnosis creates a new stable sequence/event identity. Capture receipt sequence
and diagnosis without changing recipients of historical events. Legacy receipts
can gain current guidance on their next meaningful capture.

Append `initial_evidence_repaired`, `initial_recovery_blocked` and
`initial_recovery_cleared` to the existing durable history ledger atomically with
the corresponding receipts, including no-receiver captures. Repairs retain the
trusted actor, operator reason and actual verified outcome. Alert records link
the initial failure, review, job, run, generation and notification event. Replays
and unchanged observations append nothing. Old repair receipts appear as retained
observations while their review exists and no matching durable event remains.
No read backfills history; prior alert transitions cannot be reconstructed from
only the latest receipt.

An append-only migration extends the history kind constraint and validates
initial associations while preserving candidate and retry constraints. Replay
preserves retained evidence; downgrade refuses while initial history remains.
The schema snapshot and SQLC projection follow the database. Existing retention,
per-app cap, account scope, filters and fenced pagination apply.

OpenAPI and SDK evidence-kind enums add `initial`; history adds optional
`initial_failure_id`. Receivers and SDK consumers should accept the new enum
value. Existing endpoints, access/MFA, budgets and execution authority remain
unchanged. Notification and history capture release no slot, admit no child and
refund no charge. Clearing attention does not prove workflow success.

## Consequences

Initial recovery alerts lead to the correct diagnosis and repair, and shared
history can explain proof restoration and later worker progress. Prospective
history survives source review pruning within existing retention; legacy receipt
observations require a retained source review. Initial and retry evidence retain
their distinct API identities and repair paths.
