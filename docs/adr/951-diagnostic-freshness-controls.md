# ADR-951: Diagnostic freshness controls

## Status
Accepted.

## Context
Resumable diagnostics retain samples with their original timestamps. Coverage alone does not show whether those observations are recent enough for an operator's current needs.

## Decision
Add an optional maximum diagnostic age, independently of receipt evidence freshness: zero disables the policy; enabled values range from 100 milliseconds to 24 hours. CLI and monitoring options require diagnostics when a nonzero age is supplied. Standalone diagnostic helpers accept the policy directly.

Each enrichment recomputes `diagnostics_progress.freshness` counts (`fresh`, `stale`, `unread`), `freshness_checked_at`, `max_diagnostic_age_ms` and `fresh_complete`. Valid samples older than the enabled limit or dated after the local check time are stale. The boundary is inclusive: age equal to the limit is fresh. With the policy disabled, valid samples count as fresh regardless of age. Unsupported recipients are excluded; deferred recipients with retained valid samples remain classified by sample age and separately counted as deferred. Invalid or failed evidence is unread. Saved policy, counts and classifications are never trusted on resume; the current invocation supplies the policy and samples keep their original timestamps.

Keep the existing unread-first, failed-read and refresh scheduling. Within the covered refresh pool, stably partition stale samples ahead of fresh ones, preserving cyclic order within each group. This uses the existing refresh cursor and reservation, one batch per pass and the existing 50-recipient cap. A small age limit or large selection may prevent all samples from being fresh simultaneously.

## Consequences
Historical coverage `complete` and `remaining` keep their existing meaning. `fresh_complete` means every relevant target has a valid sample within the active policy at the recorded check time; it is not a guarantee about current routing, handler completion or delivery. Neither diagnostic freshness nor coverage gates recovery settlement, command exit success or receipt observation freshness. Checkpoints preserve timestamps and cursors, while resumed invocations recompute freshness. No backend endpoint, SQL, migration or compliance registry changes are required.
