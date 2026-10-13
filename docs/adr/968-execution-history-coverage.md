# ADR-968: Execution-history coverage tracking

## Status
Accepted. Amends ADR-967 execution evidence eligibility.

## Context
A retained receipt snapshot alone cannot establish whether a subscription has lost execution evidence. Receipt deletion removes root associations; invocation deletion cascades attempt history; replay can replace terminal state. A moving deletion watermark cannot prove later windows complete when older roots may still execute or replay.

## Decision
Persist one coverage record per subscription independently of its live definition, with account/app binding, collection start, known-baseline flag and first evidence-loss time/reason. The record belongs to the app and survives subscription deletion. New subscription inserts create known baselines transactionally. Migration seeds existing subscriptions and retained delivery controls as unknown baselines; it does not invent historical coverage or reset existing records.

Track receipt and materialized recipient deletion against their subscription IDs. Track invocation deletion, attempt deletion and execution replacement transactionally with database triggers. Because deleted invocation associations cannot reliably identify a subscription, these losses conservatively invalidate all existing coverage records for the app. Replacement includes replay generation, root/parent, identity/creation changes and replacing a completed state or completion timestamp. Normal dispatch transitions do not invalidate coverage. MemStore mirrors these boundaries.

Read coverage in the same repeatable-read snapshot as execution measurements. Return history_status (complete, partial, unknown), history_reason, optional coverage_started_at and coverage_lost_at. The existing history_complete boolean is true only when the baseline is known, the requested window begins at or after collection start, no persistent evidence loss exists, window attempt evidence has not expired, and there is no truncation, missing root or unknown outcome. Empty intact windows may be complete, but SLO minimum traffic still prevents eligibility. Attempt expiry checks are conservatively scoped to the app even before physical pruning.

Persistent loss is partial and never clears merely because the requested window moves forward. Legacy baselines remain unknown. Reconciliation/backfill of durable root associations is needed to restore eligibility; no reset endpoint is added. New subscription IDs establish independent new baselines. These semantics intentionally prefer unknown/partial over a false completeness claim.

Expose diagnostics in execution and combined delivery-health responses, CLI summaries and generated Node/Python SDKs, with matching Go DTOs. Execution SLO observations reuse the coverage reason when ineligible. Existing endpoints, measurement scope, retention policies, ordering and deduplication are unchanged.

## Consequences
Intact, newly instrumented subscriptions can enable execution SLO alerts once the entire window is covered. Legacy subscriptions require reconciliation, and unrelated invocation evidence loss within the same app can conservatively disable eligibility. This release tracks evidence loss rather than preserving erased execution measurements. No local tests or compliance registry changes are included.
