# ADR-964: Waiting-reason delivery-health watch alerts

## Status
Accepted.

## Context
Complete pending waiting-reason counts (ADR-963) expose blockers across large backlogs. The invocation-local health watch needs configurable alerts for these reasons.

## Decision
Add Go watch options WaitingReasonCounts and WaitingReasonDurations keyed by the existing known reasons. Count thresholds are positive integers up to the existing watch count maximum; duration thresholds use the existing 100ms–24h bounds. Empty maps disable these conditions. Unknown reasons or invalid thresholds fail validation before polling. Conditions are ordered deterministically by reason, with counts before durations. Their metrics are waiting_reason.REASON.recipients and waiting_reason.REASON.seconds, permitting both thresholds for one reason without identity collisions.

Add repeatable CLI --waiting-reason-count REASON=N and --waiting-reason-duration REASON=DURATION flags, requiring --watch and rejecting duplicate reason entries within each flag kind.

Use only the complete, correctly bound all_pending_application_recipients aggregate, with its own valid acceptance cutoff and strictly advancing fresh observation timestamp. Require an exact known-key, nonnegative partition against its own pending total. Missing, incomplete, stale, inconsistent or mismatched evidence loses confirmation; it never falls back to the bounded event sample or independently read routing totals. Counts exceeding the exact supported numeric range are unavailable for count conditions.

Count alerts breach inclusively. Duration alerts measure consecutive fresh local observations with a nonzero count for the reason, require at least two observations, and restart after evidence loss or a zero count. This measures persistence of a reason at subscription scope, not the same recipients or uninterrupted real-world blockage between polls. Existing opened, resolved, and confirmation_lost transitions, incident identities, callback isolation, and invocation-local notification semantics apply unchanged. Fresh complete zero counts clear confirmed alerts, regardless of recipient sample truncation.

## Consequences
Go API/SDK and CLI gain reason-specific monitoring without backend queries, migrations, server alert rules, automatic recovery, durable delivery, or changes to compliance registries. Node and Python continue to expose the summary aggregates; they do not gain a local watch helper in this change.
