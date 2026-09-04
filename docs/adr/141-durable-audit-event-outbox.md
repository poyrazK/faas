# ADR-141 · Durable imaged→apid audit-event delivery

- **Status:** accepted
- **Date:** 2026-09-03
- **Deciders:** Gregale platform team
- **Related:** issue #472 / ADR-058, ADR-064, migration 00590

## Context

The imaged daemon discovers image-signature failures, but the original
handoff to apid was only `pg_notify('audit_event', payload)`. PostgreSQL
`LISTEN/NOTIFY` is a useful low-latency wakeup, not a durable queue: a
reconnect window or an apid restart could lose the only copy of a
`app.signature_missing` or `app.signature_invalid` audit event.

## Decision

Add `audit_event_outbox` in migration 00590. Imaged inserts a JSON payload
under a deployment-scoped dedupe key, then publishes the existing
`audit_event` notification with the outbox ID. Apid handles that notification
by delivering the referenced row; an apid worker also claims and replays
pending or expired-lease rows every two seconds, so delivery does not depend
on the notification arriving.

Delivery inserts the append-only `events` row and marks the outbox row
`delivered` in one transaction. `events.outbox_id` plus a partial unique index
prevents duplicate audit rows. Failed deliveries use a five-second capped
exponential backoff, dead-letter after twelve recorded failures, and retain
their error text up to 2 KiB. A daily apid cleanup removes terminal outbox
metadata older than 90 days; the audit row survives because the foreign key
uses `ON DELETE SET NULL`.

The capability is additive and optional at the Go interface boundary. Older
test or integration stores can continue implementing `state.Store` without
adding queue methods; PgStore is the production implementation and MemStore
mirrors the state machine for deterministic tests.

## Consequences

- Signature audit delivery is recoverable across notification loss and apid
  restarts.
- Dedupe is stable per deployment and event kind, so imaged retries are safe.
- The existing notification channel and legacy payload path remain compatible
  with older imaged producers; when enqueue is unavailable, imaged falls back
  to the original best-effort notification.
- The typed `wake.deploy_failed` projection is emitted once by whichever apid
  path wins the durable delivery race. The audit row remains the durable
  source of truth.
- The outbox is intentionally limited to the imaged signature-audit bridge.
  Other cross-daemon paths should adopt their existing durable row/queue
  primitives or a follow-up outbox design rather than silently sharing this
  schema.

## Rejected alternatives

- **Rely on LISTEN/NOTIFY reconnects:** reconnects reduce downtime but cannot
  recover notifications already missed.
- **Poll `events` for missing signature rows:** there is no durable source to
  poll when the notification is the only writer trigger.
- **Put all audit writes behind a general Store outbox interface:** that would
  break narrow Store implementations and broaden this focused release gate;
  the optional capability keeps the migration incremental.
