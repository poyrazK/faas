# ADR-924: Recovery notification retry outcome summaries

Date: 2026-10-09
Status: Accepted

## Context

Retry history lists original queued/skipped counts, while determining which requests remain unresolved requires opening each request. Later delivery generations cannot establish an earlier request's outcome.

## Decision

Extend each history summary with succeeded_count, failed_count, pending_count, unknown_count, status, evidence_complete, and optional completed_at. Count only the originally queued generations using the existing detail outcome rules. Skipped targets remain outside outcome counts. Known failure takes precedence, followed by unknown evidence or no queued targets (inconclusive), then pending, then succeeded. Success requires at least one queued target and all queued targets succeeded.

Evidence is complete only when every queued target has a known outcome and a complete retained attempt sequence. All-skipped requests have complete empty evidence but are inconclusive. Completion time is the latest retained terminal attempt finish time, present only when every queued target has a known terminal outcome. A failed request with other pending/unknown receivers has no completion time. A proven terminal outcome can remain known even when earlier attempt evidence is incomplete.

Read saved decisions and existing notification selection evidence in the existing repeatable-read read-only transaction or memory lock. Postgres aggregates all distinct, proven delivery/generation targets in one SQLC query, scoped to account, app, webhook, delivery, and exact source event; it does not issue a query per target. Receipts and targets remain bounded by existing limits. Memory shares its detail enrichment logic. Missing delivery or terminal evidence remains unknown. The history observed_at timestamp applies to every summary's current evidence; decided_at remains immutable.

Expose the fields in the Go, Node, and Python SDKs and the existing CLI history list. Existing authorization, MFA, query rejection, timeout, metadata-only response, ordering, and job retention remain unchanged. Reads never mutate durable receipts or deliveries.

## Consequences

Operators can identify unresolved requests from one history read. Later success cannot conceal an earlier generation's failure, and retention gaps remain visible. No migration, additional retention, new endpoint, dispatch, or automatic retry behavior is introduced.
