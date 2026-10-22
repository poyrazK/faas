# ADR 1087: Python combined realtime readiness gate

Status: Accepted; runtime validation deferred.

## Context

Applications need one gate for current transport readiness, selected subscription acknowledgements and successfully processed replay, without sequential timeouts or overlooking a completed stage invalidated during another wait.

## Decision

Export RealtimeCatchUpRequirement, RealtimeReadinessResult, sanitized RealtimeReadinessError and wait_realtime_ready from a generator-preserved module. Bind fixed connection/subscription/catch-up generations to one endpoint/account/loop. Select all or specified requested subscriptions and up to 32 distinct matching catch-up streams. Reuse existing stage validators/results while observing all dependencies through bounded waiter futures.

Use a single optional deadline (None or finite 0–86400) and existing fail-on-stall semantics. Wake at configured catch-up stall deadlines without polling. Observe connection transitions through internal readiness listeners, ignore no completed-stage invalidation, and recheck current state/generation before success. Typed stage/reason failures cover disconnected/obsolete/disposed, denied, resync and stalled. Offline is terminal rather than following later retries silently.

Bound listeners by existing tracker capacities and 128 connection gate listeners. Clean registrations on success, failure, timeout and cancellation without cancelling tracker/transport work. Add no network or background polling task. Preserve SDK generation/export rules.

## Consequences

Readiness is a point-in-time observation, not future delivery assurance. Caller supplies authoritative replay targets, ordered processing and actual transport/subscription events. No routes, grants, schema or migration changes. Static lint/compilation, SDK AST/export/preservation checks and diff checks verify implementation; runtime validation remains deferred by user request.
