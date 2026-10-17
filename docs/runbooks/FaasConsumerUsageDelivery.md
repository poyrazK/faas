# Financial usage delivery

The gateway journals request usage under `/var/lib/faas/consumer-usage` and replays the oldest unacknowledged event to apid. The apid transaction is idempotent by event ID. **Do not delete, truncate, or manually advance the journal or cursor** to clear an alert; doing so loses financial evidence.

## Symptom

The pending-record or pending-byte gauge keeps rising, delivery failures increase, or gatewayd-internal fails `/readyz` as its outbox reaches 90% capacity. Consumer- and tenant-attributed responses wait for their usage event before they complete (ADR-1505). When the outbox cannot take one, the gateway withholds the response and counts `gateway_usage_commit_aborts_total`: clients see truncated responses or closed connections, and those requests are not billed. Append failures for anonymous or audit-only traffic do not withhold responses.

## Check

Check `gateway_consumer_usage_outbox_pending_records`, `_pending_bytes`, `_failures_total`, `gateway_usage_commit_aborts_total`, `gateway_consumer_usage_delivery_failures_total`, and `gateway_consumer_usage_delivered_total` on each gateway. Check gateway logs for `consumer usage delivery blocked`, `consumer usage outbox append failed`, or `consumer usage commit failed`. Verify that apid's private request-telemetry gRPC listener is reachable and that Postgres accepts writes. The debugger can be disabled; the accounting RPC should still be available.

## Recover

Restore the listener or database, then watch pending records drain and delivered events rise. Leave the journal intact throughout. A lost response may replay an event, but apid's event ledger deduplicates it. If one event repeatedly returns a validation or foreign-key error, preserve a copy of the spool and investigate the referenced account/app/tenant lifecycle before any manual repair. FIFO delivery intentionally stops rather than silently discarding that event.

Deploy apid before gatewayd-internal. A new production gateway checks for `RecordConsumerUsage` at startup and refuses to run against an older apid, preventing duplicate ledger increments from collapsed debugger rows. See [ADR-234](../adr/234-durable-consumer-usage-delivery.md).

## Reconcile

Monetized usage is complete by construction: a consumer- or tenant-attributed response cannot complete before its event is fsynced, and a withheld response is not billed. If `_failures_total` increased without matching `gateway_usage_commit_aborts_total`, the failures were anonymous or audit-only events. They affect request evidence and discovered routes, not customer bills. A local disk failure after fsync but before delivery still needs the spool preserved.
