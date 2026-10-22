# ADR-963: Complete subscription waiting-reason counts

## Status
Accepted.

## Context
ADR-962 exposed complete effective capacity counts, but other blockers still required inspecting a bounded event sample. Operators need a reason partition across large pending backlogs.

## Decision
Extend the existing backlog consumer query with pending_waiting_reasons, a fixed dictionary of known effective reasons, including zero counts. PostgreSQL uses filtered counts after the existing observation classification; MemStore increments the same classified reason only for pending recipients. Counts are independent of recipient pagination and respect the backlog's existing filters. Processing recipients are excluded. No classification precedence or delivery state changes are introduced.

Add optional waiting_reasons to subscription delivery health with app/subscription identity, observed_at, window_at acceptance cutoff, coverage, completeness, pending total, and counts. Populate it from the same backlog snapshot as the existing capacity aggregates and investigative sample. Require a known-key, nonnegative, overflow-safe exact partition whose sum equals pending_recipients before marking it complete. Empty matching backlogs provide a complete zero dictionary. Unknown or inconsistent PostgreSQL aggregate shapes fail the read rather than synthesizing healthy zero counts. Older servers may omit the optional aggregate; absence means unavailable evidence.

The dictionary covers circuit restrictions, subscription pause/rate limiting, ordering, capacity scopes, receipt processing, retry backoff, workflow routing, routing-in-progress, and ready classification. Known reasons that cannot occur in the selected pending application scope remain zero. Each recipient contributes exactly once after existing control and ordering precedence. Ready means the classifier found no higher-priority wait, not that a handler succeeded or a target is available. These are retained captured/backfill backlog observations, not historical totals or predictions.

## Consequences
API, CLI, and Go/Node/Python SDKs expose complete reason counts without additional queries, unbounded recipient downloads, new quotas, migrations, or compliance registry changes. The pending sample remains useful for exact-event investigation. Other health components remain independent snapshots; reconcile reason counts only against this aggregate's own pending total and use its timestamps for freshness. Existing capacity watch behavior and server alert rules remain unchanged.
