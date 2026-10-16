# ADR 1077: Python read-refresh coordinator

Status: Accepted; runtime validation deferred.

## Context

Read-progress freshness hooks identify stale streams, but application polling can create overlapping or unbounded refresh requests and apply late responses after reconnect reset.

## Decision

Export RealtimeReadRefreshCoordinator and sanitized RealtimeReadRefreshError in a generator-preserved handwritten module. Accept a tracker and authenticated async fetcher returning a confirmed receipt for the requested identity. Bind to one endpoint/account and asyncio loop. Bound total queued/active stream-reader identities to 1024 (1–8192), concurrent requests to 4 (1–32), and timeout to five seconds (0.1–60).

Validate requests before atomically checking capacity. Coalesce queued identities to one input and in-flight repeats without follow-up fetches. Own at most concurrency tasks; completion starts queued work. Validate response identity and nonregressing requested bounds before applying through tracker monotonic rules. Errors leave tracker state unchanged and invoke an optional synchronous sanitized callback; do not automatically retry.

Track reset/dispose generation in the tracker. Ignore previously admitted responses and queued work after generation changes, preventing stale reconnect responses from repopulating cleared views. A scoped reset invalidates all earlier coordinator generations conservatively.

wait_idle joins admitted work without propagating per-request errors; cancelling it leaves requests running. aclose permanently rejects admission, drops the queue, cancels requests and joins tasks even during repeated cancellation. Caller transport must cooperate with cancellation. Preserve exports/module through SDK generation.

## Consequences

Freshness hooks can drive bounded refreshes without adding another connection/polling layer. No server read writes or guessed identity mapping is introduced. Failed/in-flight-coalesced requests can be retriggered on a later application poll. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation checks and diff checks verify implementation; runtime validation remains deferred by user request.
