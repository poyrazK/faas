# ADR-1402 · Graceful application outbox recovery worker

Date: 2026-10-15
Status: Accepted

## Context

The SDK exposes one recovery iteration per call. Applications otherwise repeat
polling, concurrency, transient-error handling and shutdown coordination. Canceling
publication immediately on process shutdown can prevent known acceptance from
being settled locally, while continuing to claim new rows prolongs shutdown.

## Decision

Add an explicit blocking Go worker pool with bounded concurrency, exponential
idle polling backoff, per-operation deadlines and local outcome callbacks. Reuse
the existing claim, retry-policy and fenced publication paths. Split the internal
claim phase from publication so claim SQL follows parent cancellation while
committed claims can drain through a deliberately detached context.

On parent cancellation, stop claims, allow a bounded grace interval for in-flight
publication/settlement/callbacks, then cancel the drain context. Wait for every
worker goroutine to finish; require cooperative database drivers, transports and
callbacks rather than abandoning work. Preserve direct single-iteration recovery
behavior and all existing publication key, deadline and lease fences.

Callbacks are synchronous per worker and concurrent across the pool. Emit typed,
sanitized metadata for meaningful outcomes, not empty polling. Report unknown
commits and settlement failures; suppress ordinary claim cancellation at shutdown.
No schema, platform daemon, notification transport or automatic DDL is added.

## Consequences

Applications can adopt reliable recovery without building a polling pool. They
still own process supervision, connection capacity, policy consistency and durable
observability. Slow or noncooperative dependencies can delay shutdown; the grace
interval requests cancellation rather than terminating goroutines. Callback
results are observations, not consumer-completion or exactly-once guarantees.

See [configuration and shutdown contract](../event-outbox-worker.md).
