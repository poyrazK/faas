# ADR 1081: Python realtime health summary

Status: Accepted; runtime validation deferred.

## Context

Python status, quality, presence and unread trackers expose separate observations. Apps repeat readiness classification and cannot distinguish empty completed presence snapshots from missing snapshots.

## Decision

Export immutable RealtimeHealthSummary and summarize_realtime_health from a generator-preserved module. Project one endpoint/account's current snapshots into offline/connecting/reconnecting/delayed/updating/live states with English labels and structured reasons. Connection state takes priority, then degraded quality, then unknown quality/unverified unread/pending presence. Keep history gaps as a separate warning. Quality must match the current attempt; older observations are excluded. Omitted optional inputs remain untracked.

Expose structured unread badge, waiting presence channels, optional readiness and per-channel summed viewer counts. Validate observations/bounds and duplicate channels. Do not own callbacks, polling or transport. PresenceSummary gains ready default false; directory completed snapshots set readiness, pending chunks clear the projected readiness while preserving visible data. Notify on staging/readiness changes and reset even for empty channels. Connection outages make supplied presence waiting.

## Consequences

Apps can render a consistent health view while retaining independent warnings. Live refers only to supplied observations, not global completeness. Callers reset presence on reconnect, refresh observations each tick and keep account/endpoint sources isolated. The helper cannot infer prior-attempt presence when reset is skipped. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation checks and diff checks verify implementation; runtime validation remains deferred by user request.
