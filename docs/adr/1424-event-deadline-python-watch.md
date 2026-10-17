# ADR-1424 · Async Python deadline count watches

Date: 2026-10-16
Status: Accepted

## Context

Python exposes deadline-window reads, while Go/CLI and Node also offer sustained
count alerts, explicit recovery boundaries and notification checkpoint handling.

## Decision

Add an async Python deadline-only watch with independent total/reason conditions,
strict safe-integer counts, complete scoped evidence validation and monotonic
confirmation durations. Inspect raw JSON timestamps to retain nanosecond ordering
beyond generated datetime precision. Treat failed, stale, regressing, repeated or
incomplete evidence as unknown and reset confirmation instead of resolving.

Use a distinct Python versioned checkpoint kind. Bind resume to effective target
and policy, keep incident identities and bounded alert/acknowledgment history, and
discard live proof on resume. Reserve closing capacity for active incidents and
validate acknowledgment batches atomically. Keep opened/resolved lifecycle alerts
separate from unknown progress diagnostics.

Use native asyncio task cancellation, propagating a CancelledError subclass with
the latest recoverable state. Await detached async callbacks serially; ordinary
callback errors also expose recoverable state. Applications own persistence,
notifications, cancellation-safe shutdown and downstream deduplication. Reuse the
existing generated read operation and add no endpoint or schema changes.

## Consequences

Python applications can monitor complete deadline cohorts without implementing
polling, timer proof or durable alert bookkeeping. Python checkpoints intentionally
do not transfer to Go/CLI or Node. Notification delivery remains at least once and
requires saving checkpoints before publishing. Callback duration may extend watch
timeout, while reads/sleeps remain bounded and task-cancellable. Aggregate recovery
does not prove business completion or change delivery/retention policy.

See [usage and limitations](../event-deadline-python-watch.md).
