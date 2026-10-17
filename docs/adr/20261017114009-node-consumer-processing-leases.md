# ADR: Node consumer processing leases

- Status: Accepted
- Date: 2026-10-17

## Context

Node receipts deduplicate transactional business writes and durable acknowledgment
intents recover callbacks. Preparation may need to run outside a long database
transaction while duplicate workers and crash takeover still require a local
commit fence. The Go SDK already defines an application-owned processing table,
consumer identity lock, token fencing and expiry semantics.

## Decision

Package the existing Go processing schema byte-for-byte with the receipt contract
generator. Add Node claim, renew and complete capabilities using the same identity
and original-input digests, random 32-byte token and database-clock expiry. Claim
serializes on the consumer lock, recognizes completed receipts and replaces only
expired claims without changing their immutable request digest. Unknown claim
commits return no capability.

Add internal transaction hooks only for leased calls. Check and lock the claim
before the business callback and conditionally remove it after receipt insertion.
The delete is the local completion linearization point, matching Go; optional
intent staging follows before the same transaction commits. A failure rolls back
writes, receipt, claim removal and intent together. Receipt replay validates
stored identity/input/result and needs no live processing row.

Integrate immediate and durable acknowledgment workers with an independently
acquired matching capability, preserving the receipt-only probe and existing
platform renewal behavior. Serialize operations on each Node capability and redact
formatting/JSON and SQL diagnostics. Positive whole-millisecond durations must fit
Go time.Duration. Cancellation remains cooperative; once COMMIT is sent, preserve
confirmed or unknown outcomes and discard an uncertain connection.

## Consequences

No platform schema, route, registry or migration changes are needed. Ordinary
unleased helpers never consult the optional processing table. Local processing
and platform acknowledgment leases protect separate boundaries. The application
owns side-effect-free preparation, local renewal cadence, platform renewal during
preparation, bounded driver calls, durable job ownership and retention. Stop and
join local renewal before the short final transaction. External effects still
need idempotency or an outbox; lease expiry cannot physically stop an old process.

The explicit version 3 example selects leased processing without reinterpreting
old unleased durable jobs. Helpers install no service, schema or retry scheduler.
See the [Node processing guide](../event-consumer-processing-node.md).

## Validation

Use Node build/generation, schema and SQL source parity, package/export inclusion
and static documentation checks. No new tests, assertion changes or local test
runs are included. Hosted acceptance remains necessary for rollback, takeover,
expiry, concurrent claims and shared Go/Node state before release.
