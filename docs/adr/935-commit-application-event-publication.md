# ADR-935: Commit application event publication

Date: 2026-10-10
Status: Accepted; production qualification pending
Related: ADR-430, ADR-589, ADR-613, ADR-934

## Context

Commit currently delivers a customer outbox row to one managed Operation. An
application needs to update business rows and emit an event for independent
subscribers without a publish-after-commit crash gap. Event receipt retention
must not cause a delayed Commit relay retry to emit that event again.

## Decision

Introduce an immutable `event` Commit destination with contract version 3. The
bound app is the publisher, not an invocation destination. Only account-scoped
publisher applications are supported; operation policies, customer selection and
per-event operation routing are rejected. Existing operation destinations and
versions 1 and 2 retain their contract.

The owner installs the publication checkpoint upgrade in its existing outbox.
SDK inserts share the business/consumer transaction and may specify a registered
schema version. The existing scheduler relay observes committed rows and calls
the same atomic acceptance owner as the HTTP API.

Use CloudEvents source `app.<app UUID>` and event ID
`commit.<Commit source UUID>.<outbox event UUID>`, with canonical UUID spelling.
Commit source identities are independent even when event UUIDs repeat. Replay
compares the original event type, schema version and semantic JSON data before
pause, current schema or storage-capacity checks. New acceptance applies the
publisher schema contract and existing retained-event count/byte quotas.

In one platform PostgreSQL transaction, record the event ledger, captured
subscription snapshot, storage accounting and a Commit receipt containing the
acceptance ID, event source/ID and acceptance time. Commit receipts have no
foreign key to the normally retained event receipt. After event retention removes
routing evidence, relay retries return the original Commit receipt and never
publish again. Subscriber delivery uses the existing fanout scheduler and each
subscriber's independent retry, retention, replay and dead-letter behavior.

Publication receipts expose an event receipt URL with an acceptance-time guard.
The read checks this guard against the same receipt snapshot and returns 409 on
replacement. CLI inspection shows bounded subscriber evidence and its pagination;
Commit wait supports publication acceptance, not an invented aggregate execution
completion. Normal event receipt retention remains distinct from retained Commit
acceptance. Notifications are wake hints, not the durable acceptance boundary.

## Consequences

A customer transaction commit, platform publication acceptance, and each
subscriber's execution completion are separate facts. Producer and consumer
SDK transactions compose without a new daemon. Delivery remains at least once;
consumer receipts and external-effect idempotency remain necessary. Independent
subscriptions have no shared execution order; existing configured per-consumer
ordering rules apply. Relay acceptance is not ordered by customer transaction
commit time. Retaining Commit identities continues the existing
indefinite-history storage obligation.

Customer schema installation/upgrades remain explicit owner actions. No routing
upgrade or migration is performed by the relay. Source destination/name replay
cannot retarget an existing source. Version 3 sources require the publication
checkpoint columns before the relay claims work. Downgrade refuses existing
publication sources/receipts instead of inventing operation identities.

The existing Commit qualification and production promotion gates remain in
force. Static/build checks alone do not qualify the new destination for customer
use; crash, concurrent relay, retention and native delivery gates must cover it
before promotion.
