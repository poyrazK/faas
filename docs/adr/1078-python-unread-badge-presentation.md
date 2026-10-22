# ADR 1078: Python unread badge presentation

Status: Accepted; runtime validation deferred.

## Context

Unread summaries expose counts, freshness, missing streams and retention gaps. UI formatting can accidentally present unavailable data as confirmed zero or omit accessibility caveats.

## Decision

Export immutable RealtimeUnreadBadge and format_realtime_unread_badge from the existing module and generator barrel. Cap display counts at configurable max_count (99 default, strict integer 1–999999) while retaining exact totals and accessible singular/plural text. Hide confirmed zero unless show_zero or a history gap requires visibility.

Show Updating… when requested coverage is incomplete/unknown or freshness metadata is unknown/stale. Preserve capped known count separately. This flag is presentation only, not a refresh request. Expose retained-history gap independently and explain count limitations in accessible text. Validate summary identities/duplicates, options and metadata membership before formatting.

## Consequences

UI can distinguish known counts, confirmed zero and unverified values without guessed state. English convenience strings are localizable using structured fields. No timers, requests, routes, grants, schema or migration change. Static lint/compilation, SDK AST/export and diff checks verify implementation; runtime validation remains deferred by user request.
