# ADR-1267: Verified automatic retry evidence repair

## Status

Accepted, 2026-10-13.

## Context

ADR-1266 provides an operator repair path for automatic first recovery. Applied
saved-failure retry actions need the same path without following a newer run or
replacing first-result-wins evidence.

## Decision

Add diagnosis and repair under
`/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence`
(GET) and `/evidence/repair` (POST). Diagnosis uses read scope and MFA; repair uses
deploy write and MFA. Use the shared repair request and receipt types, plus a
retry diagnosis containing the action ID and exact planned resume generation.

Resolve the action from its retained review and validate its immutable plan with
the existing automatic retry action validator against the frozen policy snapshot.
Only applied automatic retry actions are repairable. Pending or suppressed actions
cannot use repair as an admission shortcut. Retained unresolved older versions
remain repairable across policy mode changes, without granting new authority.

Share terminal generation, admission history, timestamp and no-overwrite checks
with first recovery. The expected generation is the frozen target count plus one.
Additionally require the child request ID to match the automatic action, the parent
job and position to match the frozen plan, and the child workflow/run/count and
single-item selection to agree. Re-inspect under account-first policy/action locks.
PostgreSQL share-locks the live run and guards generation, workflow, parent and
request identity in the result insertion itself. Normal capture racing with repair
preserves the first result. Missing, future, superseded, conflicting or incomplete
proof remains blocked.

Store one immutable audit receipt per action in the retained review JSON's
`evidence_repairs` map. The receipt contains request UUID, operator reason, actor,
time and verified outcome. Identical request ID/reason retries return the receipt;
changed requests conflict. Ledger insertion and receipt capture are atomic.
JSON key updates preserve concurrent review decisions and other action receipts.
No new schema columns or migration are required. Read endpoints never capture.

Wake the existing automatic worker without changing enablement, pauses, outcome
counters, budgets or outstanding slots directly. The normal worker confirms repaired
proof and performs its existing version-fenced result accounting. Native completion
and existing retry notification semantics remain unchanged.

## Consequences

Both automation modes have a verified repair path and durable operator receipts.
No input is exposed in diagnosis. Repair cannot infer a prior generation from a
newer run, overwrite existing evidence, rebuild pruned lineage or force-clear a
slot. Disabled or paused policies retain their controls. Two route registrations
add two server baseline lines; all feature logic stays in appevents and state.
