# ADR 1076: Python read-progress freshness

Status: Accepted; runtime validation deferred.

## Context

Confirmed unread counts may become stale after disconnect or prolonged silence. Aggregated badges need freshness indicators without inventing counts or taking over transport refresh.

## Decision

Add optional stale_after_seconds (None default, finite non-boolean 0.1–86400) and synchronous on_refresh to the read-progress tracker. Store monotonic arrival times per existing bounded identity. Valid identical receipts renew freshness while preserving apply's False return for unchanged progress; notify on stale-to-fresh transitions. Backward bounds never renew freshness. Export immutable RealtimeReadProgressFreshness with progress/age/stale.

Expose freshness_snapshot, mark_stale and refresh_stale with existing channel/inbox filters. Force stale on disconnect while retaining confirmed values. Clear forced staleness only on valid receipt; requesting refresh does not clear it. Call on_refresh with stale receipts so the application can enqueue authenticated read-state refreshes, isolating callback errors. Reset/dispose remove freshness memory. Use no background timers, requests or automatic mark-read writes; evaluate age on snapshot/refresh calls driven by existing application lifecycle.

Extend unread aggregation with optional freshness metadata and stale_streams. Retain confirmed totals; unknown/mismatched metadata for a selected stream counts as stale. Omitted metadata produces None, expressing unknown freshness. Keep requested-stream completeness and retained-history gaps independent. Validate/bound metadata and reject duplicates.

## Consequences

UI can distinguish fresh zero from stale counts and trigger its own refresh path. Captured metadata is point-in-time and should be regenerated at later render ticks. Applications debounce repeated refresh hooks; no transport policy is imposed. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export checks and diff checks verify implementation; runtime validation remains deferred by user request.
