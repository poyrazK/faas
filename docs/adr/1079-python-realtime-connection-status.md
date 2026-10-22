# ADR 1079: Python realtime connection-status observation

Status: Accepted; runtime validation deferred.

## Context

Python realtime consumers own their transport/reconnect lifecycle. UI needs connection state and stale-view signalling without introducing another connection controller.

## Decision

Export immutable RealtimeConnectionStatus and a generator-preserved RealtimeConnectionStatusTracker. Observe initial offline, first connecting, subsequent reconnecting, confirmed connected and failure offline states. Each begin_connect issues a monotonically increasing local attempt ID. connected/disconnected require that ID and ignore stale/duplicate callbacks; offline cannot be revived without another attempt. Count retries since success and retain the last successful aware UTC timestamp.

Expose views_stale for nonconnected transport state, not as proof of data freshness. An optional synchronous on_invalidate hook runs before nonconnected status notification, letting callers mark retained read counts stale and reset ephemeral presence. Keep callbacks nonblocking/idempotent and isolate exceptions/callback cancellation. Guard status notification against reentrant invalidation transitions. Dispose leaves offline status and rejects new attempts while ignoring late callbacks.

## Consequences

Applications still own transport authentication/readiness, retries, refresh cancellation and data rebuilding. Successful connection alone never clears receipt stale flags or restores presence. No sockets, network tasks, timers, grants, schema or migration changes. Static lint/compilation, SDK AST/export/preservation checks and diff checks verify implementation; runtime validation remains deferred by user request.
