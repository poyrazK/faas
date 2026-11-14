# ADR-20261105081859: Managed PostgreSQL rotations retain a durable runtime handoff

- **Status:** accepted (owner, 2026-11-05: solve the managed PostgreSQL review findings)
- **Date:** 2026-11-05
- **Related:** [ADR-284](284-safe-object-storage-binding-rotation.md), [ADR-20261029174950](20261029174950-managed-postgres-delete-grace-period.md)

## Context

A credential rotation could fail during provider preparation and later finish
through the binding worker. Only the HTTP handler queued the runtime refresh,
so that recovery published generation 2 without scheduling the refresh that
authorizes retiring generation 1. A crash between publication and the handler's
notification had the same result.

## Decision

1. PostgreSQL commits a serving credential's ready receipt and its
   `notification_outbox` runtime-refresh event in one transaction. The sealed
   app secret and its snapshot-invalidating config stamp are already committed
   before this transition. A failed outbox insert rolls back the ready receipt;
   recovery reuses the same credential generation and wake ID.
2. The app lock precedes the binding lock. Concurrent workers and API retries
   share the same app/wake event. The HTTP handler no longer claims an app
   restart or publishes a second event after the service returns.
3. Ready rotations with no active refresh event remain discoverable, even when
   new provisioning is closed. This repairs rows written by N-1 workers. A
   terminal event without a retirement acknowledgement is replayed under its
   original wake ID; an active event uses the existing scheduler outbox retry.
4. Serving credentials overlap until the scheduler confirms that exact wake
   ID. If the app has neither a live deployment nor resident instances, no
   runtime needs the old credential and cleanup can proceed without a refresh.
   Migration credentials retain their existing task-drain retirement barrier.
5. MemoryStore models the same queued intents for local wiring and tests.
   Custom serving-binding stores must support the durable refresh capability
   before admitting a rotation.

An expand migration builds a partial index concurrently for ready rotations
waiting on a refresh. No new catalog columns or API fields are required. The
existing outbox index supports app/wake lookup, and old scheduler workers accept
the unchanged refresh payload. Qualification against live providers remains an
operator rollout step.
