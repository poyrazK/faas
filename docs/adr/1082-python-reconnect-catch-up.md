# ADR 1082: Python reconnect catch-up observation

Status: Accepted; runtime validation deferred.

## Context

A ready transport does not prove application replay is processed. Existing subscription acknowledgements expose a cursor, not an authoritative replay target.

## Decision

Export generator-preserved RealtimeCatchUpStatus and RealtimeCatchUpTracker for one channel/inbox. Freeze an explicitly supplied authoritative target, or wait for one. Observe successfully processed ordered positions only. Expose remaining sequence distance, never inferred message counts. Ignore duplicate/backward processing and stale local generations. begin issues new generations for every subscription/recovery, including within one connection attempt, while rejecting older connection attempts.

Explicit server retained-history gaps set resync_required and block progress until the application recovers and begins again. Do not replay, ACK, advance read state or silently resync. Isolate synchronous monitoring callbacks. Extend health projection with current-attempt catching-up and resync states; older-attempt/unknown targets remain updating. Connection state takes priority, followed by resync, degraded quality and catching-up.

## Consequences

Callers supply authoritative target, ordered processing/fencing/deduplication and real recovery. A caught-up frozen target does not guarantee no later live messages. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation and diff checks verify implementation; runtime validation remains deferred by user request.
