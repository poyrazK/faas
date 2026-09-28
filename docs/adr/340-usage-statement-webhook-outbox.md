# ADR-340 · Atomic usage statement webhook production

- **Status:** accepted
- **Date:** 2026-09-28
- **Extends:** ADR-076

## Decision

Finalizing an API consumer usage statement writes a `usage_statement.finalized`
event to `app_webhook_event_outbox` in the same Postgres transaction as the
statement status change. The event contains the finalized payload and a snapshot
of the enabled, matching app webhook IDs. A statement with no matching webhook
commits without an outbox row. The request attempts a fast relay after commit;
the schedd webhook dispatcher drains the durable outbox on each tick for crash
recovery.

The relay locks a bounded batch with `FOR UPDATE SKIP LOCKED`, inserts all
delivery ledger rows, and removes each outbox row in one transaction. A partial
unique index on `(source_event_id, webhook_id)` prevents a source event from
creating a second delivery for one subscription. A subscription registered
after finalization cannot receive the old event. A subscription deleted before
relay is skipped because its delivery row cannot reference a missing webhook.

## Why

ADR-076's `webhook.Emit` call happened after finalization committed. A crash,
request cancellation, or insert failure in that gap could leave a finalized
statement with no delivery row and no record to retry. Keeping the event write
in the source transaction closes that loss window. The inline relay preserves
the usual prompt appearance in delivery history; schedd is the recovery path
when inline relay fails or the process exits.

## Boundaries

- Delivery remains at least once over HTTP. The event/subscription key dedupes
  ledger creation, while receivers still dedupe repeated attempts by the stable
  delivery ID.
- The outbox stores only pending events. Successful fan-out deletes the source
  row in the same transaction, so it needs no separate retention sweep.
- This change covers `usage_statement.finalized`. The post-commit `app.parked`
  and `app.woken` producers need their own source-transaction integration.
- A failed relay remains visible through the schedd outbox-relay success gauge
  and alert, independently of delivery queue age.

## Verification

- A forced outbox insert failure rolls back statement finalization.
- A new store instance relays a committed event after the producing process
  exits; repeat and concurrent relays create one delivery per snapshotted
  subscription.
- The API's successful path still makes the delivery visible promptly through
  its post-commit relay.
