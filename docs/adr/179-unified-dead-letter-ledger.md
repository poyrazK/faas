# ADR-179 · Unified app-scoped dead-letter ledger and replay

- **Status:** accepted
- **Date:** 2026-09-17
- **Issue:** #1278

## Context

Gregale already has two durable failure surfaces: queue/async invocation rows
and broker trigger records with their trigger-specific dead-letter table. That
split makes it difficult for an app owner to inspect failures across sources,
and it gives each source a different replay surface. EPIC #1278 needs one
app-scoped read model without weakening the existing source state machines.

## Decision

Gregale adds `dead_letter_events` as a durable, app-scoped projection of
terminal invocation and trigger-record failures.

- The source rows remain authoritative for dispatch state. The projection is
  identified by `(source, source_id)`, preserves the source payload/headers and
  failure details, and records `replayed_at` when a redrive succeeds.
- PostgreSQL triggers capture the two existing terminal paths in the same
  transaction as the source transition. The migration also backfills rows that
  already exist, so the new read surface is complete immediately after apply.
- The public read surface is app-scoped and tenant-scoped:
  `GET /v1/apps/{slug}/dlq` lists newest-first with a cursor,
  `GET /v1/apps/{slug}/dlq/{id}` inspects one event, and
  `POST /v1/apps/{slug}/dlq/{id}/replay` atomically redrives one source row.
  The slash action form follows the existing Gregale retry/replay routes.
- Replay is idempotency-wrapped at the HTTP boundary and transactionally
  validates account/app ownership, resets the source row to `pending`, and
  stamps the projection. Existing queue- and trigger-specific endpoints remain
  available for compatibility.
- The in-memory store mirrors the projection and replay state so handler and
  state tests exercise the same tenant and pagination semantics without a
  database.

This PR deliberately establishes the ledger and single-event surface. Bulk
replay, purge/delete, success/failure destinations, CLI commands, audit/metric
families, and scheduler-owned replay orchestration remain follow-up slices of
the epic.

## Consequences

- Customers get one authenticated, app-scoped failure inventory for queue and
  trigger sources.
- Existing dispatchers and legacy APIs keep their current state machines and
  routes; the new projection is additive.
- The migration adds two bounded app indexes and a backfill cost proportional
  to existing terminal failures. Payloads remain tenant data and are returned
  only after the existing app/account authorization chain succeeds.
- Database trigger capture is intentionally centralized at the persistence
  boundary; any future failure source must add an explicit projection trigger
  or write path before it appears in the unified ledger.

## Rejected alternatives

- **Replace the source tables with the new ledger:** rejected because the
  schedulers already depend on source-specific retry, lease, and trigger
  metadata.
- **Capture asynchronously through a worker:** rejected because a crash between
  the terminal transition and the worker would lose the customer-visible
  failure record.
- **Expose only the two legacy DLQs:** rejected because callers would need
  source-specific discovery and could not build one recovery workflow.
