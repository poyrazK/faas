# ADR 1063: Python worker settlement batching

Status: Accepted; runtime validation deferred.

## Context

Concurrent Python handlers currently settle individually. Existing batch settlement routes and receipt support can reduce request overhead while preserving per-message confirmation.

## Decision

Add strict integer options `settlement_batch_size` (1–32, default 1 disables batching) and `settlement_flush_milliseconds` (1–100, default 10). Queue complete/retry settlements and flush by size or delay, serially per worker. Waiting handlers retain their concurrency slots, bounding pending work. Renewals and cooperative releases bypass the queue. Existing handler completion stops lease monitors before settlement; bounded batching delay does not extend leases and an expired lease returns lease loss.

Use existing account/scoped batch routes. Optional settlement retries use one top-level UUID per ordered logical batch; omit item keys and reuse the body for every attempt. Validate count, sequence, action and allowed status for the entire response before resolving items. Confirm valid successes independently; lease-lost entries raise for their handlers and stop the worker. Only confirmed retry/dead results invoke handler-error callbacks. Malformed/transport failures fail every item without inventing confirmations.

Caller shutdown flushes queued settlements and waits for in-flight batches. Each batch has a five-second total request/retry budget, bounding shutdown transport work. Internal failure cancels requests and discards unsent entries. Already queued or uncertain terminal settlements never lead to release. Cleanup joins the owned batch task and all waiting handler tasks. Custom transport mutation retries remain disabled.

Correct ADR 1062 request copying to use `attrs.evolve`, matching generated attrs-based DTOs rather than dataclass replacement. Share the retry loop between single and batch transports.

## Consequences

No new migration, routes, permissions or generated schema. Defaults preserve individual settlement behavior. Batching trades a bounded delay for fewer requests; low concurrency can yield single-item batches. Static lint, compilation, generated-interface inspection and diff checks verify the implementation; runtime validation remains deferred by user request.
