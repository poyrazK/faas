# ADR-1112 — Transactional realtime publishing

- **Status:** proposed, operator-gated preview
- **Date:** 2026-10-10
- **Decision:** Reuse managed Commit sources and their qualified PostgreSQL connections to dispatch a separate, explicitly installed customer realtime outbox into retained channel history.

## Why

A database commit followed by an HTTP publish has a crash window: the business change can commit without a corresponding realtime message. An application-owned outbox closes this window, but making every application operate a dispatcher defeats managed realtime's purpose.

## Contract

The application inserts into `public.gregale_realtime_outbox` using the same PostgreSQL transaction and connection as its business write. The SDK does no DDL, transaction control, or network publication. Rollback removes both writes. The existing database-owner source binding fixes the account and app; a queued endpoint must belong to that app and be enabled. Channels still require normal subscriber authorization. No channel name grants subscriber access.

The scheduler reuses the managed Commit source's sealed credentials, TLS/network policy, qualification, bounded paging, and source lifecycle. The regular invocation outbox remains separate. Only explicit customer schema installation plus `FAAS_REALTIME_TRANSACTIONAL_PREVIEW_ENABLED=1` activates dispatch; `FAAS_COMMIT_RELAY_ENABLED=true` is also required. The first version supports UTF-8 text frames, SDK JSON encoding, and the existing metadata/schema/reducer rules. Existing retained payload and channel limits apply.

Committed rows are leased with `SKIP LOCKED`. Source checkpoint writes are fenced by lease token. Transient failures retry; invalid destinations, payloads/schema/reducer inputs and identity conflicts block for owner repair. Different events can publish out of business-commit order; sequence is channel publication order. This is not ordered CDC.

The platform locks the source and endpoint, verifies ownership, and atomically commits a retained channel message and a `(source_id,event_id)` receipt with a content hash. A lost source checkpoint retries the original receipt even after channel history expires or the endpoint is deleted. Reusing an accepted identity with different content is a conflict. Receipts remain until source deletion, matching existing Commit identity retention; source recreation requires a new source ID. There is no claim of exactly-once client processing. V2 clients receive via the existing bounded history polling; raw live-only subscribers do not receive these messages.

Customer-side published rows retain inspection metadata for the configured Commit retention window, seven days by default; unaccepted rows are never automatically deleted. Source health aggregates regular and realtime backlog when the preview is enabled. Payload-free SDK database inspection reports pending/publishing/blocked/published states and sequence; retry clears only a blocked, unleased, unpublished row. The existing Commit blocked-event/replay APIs remain specific to invocation events.

## Qualification

Before promotion, exercise rollback and commit visibility with separate database connections, response loss after platform commit, lease expiry with competing dispatchers, identity conflicts after history expiry, cross-account/app destinations, schema and reducer rejection, source pause/deletion and endpoint deletion races. Apply platform migrations before enabling the preview and grant the relay role explicit permissions on the customer table. No automatic customer DDL or production promotion is included.
