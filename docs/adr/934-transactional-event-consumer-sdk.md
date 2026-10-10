# ADR-934: Transactional event consumer SDK

Date: 2026-10-10
Status: Accepted
Related: ADR-586, ADR-613, ADR-795, ADR-932

## Context

Independent recipient recovery remains at least once. A consumer can commit
business writes and lose its response before Gregale accepts completion. Producer
deduplication does not prevent repeated consumer writes. Reusing a producer
source/event ID after retention must be distinct from retrying its old acceptance.

## Decision

Carry the durable acceptance ID in new subscription invocation headers. Retries
and replay children preserve the original headers. Scope an application-owned
PostgreSQL receipt to account, app, subscription and acceptance. Compare exact
original CloudEvents body bytes, excluding volatile invocation-attempt headers.
Strip event-context headers from direct public HTTP requests. Constructors are
not authentication; apps authenticate deliveries and authorize business inputs.

The Go, Node and Python wrappers own a fresh READ COMMITTED transaction. Lock the
identity before a separate receipt read. Execute the callback only when the receipt is missing;
insert its receipt and commit with business writes. Replays return exact saved
JSON bytes. Scope/content conflicts and database errors fail without invoking the
callback. Commit acknowledgement errors remain outcome unknown and are reconciled
with the same identity. No incomplete receipt or processing lease is committed.

The canonical application-owned schema and framing are documented in
[transactional event consumers](../event-consumer-transactions.md). The existing
inbox generator packages the schema and centrally defined limits for all three SDKs.
No control-plane migration or new daemon is needed. Node uses a dedicated pg pool
connection. Python supports synchronous and asynchronous psycopg connections and
requires an exclusively leased idle autocommit connection so completion cannot
return before an outer transaction commits.

The callback may enqueue follow-up managed work in the existing Commit outbox
in the same application database. Go exposes a minimal insert transaction adapter
without changing the existing *sql.Tx helper; Python provides matching synchronous
and asynchronous insert helpers with shared validation. Node's existing query
adapter already composes. Business writes, the outbox row, and the consumer receipt
commit together. Commit retains its configured destination and independent relay;
this does not add event-subscriber publication or synchronous follow-up execution.

## Consequences

One retained receipt permits one committed set of callback writes when the
callback uses only its supplied transaction and does not control its lifecycle.
Callback execution can repeat after rollback. Apps retain receipts across all
relevant retries and historical replays; pruning permits writes to repeat.
External API effects need their own idempotency.

Legacy invocations without acceptance headers fail closed. Direct inbox messages,
workflow steps, object-notification adapters and webhook receivers are outside
this first integration. Operator replay returns the original result rather than
repeating committed writes. New business intent uses a new event acceptance.
Delivery itself remains at least once.
