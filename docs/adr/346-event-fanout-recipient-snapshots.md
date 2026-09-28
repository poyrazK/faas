# ADR-346: Snapshot event fanout candidates at acceptance

- Status: Accepted
- Date: 2026-09-28
- Amends: ADR-284

## Context

ADR-284 made fanout receipts durable, but routing read the currently enabled
subscriptions when a worker processed each receipt. Disabling, deleting, or
changing a subscription during a scheduler outage could silently change the
recipients of events already accepted. A creation-time check prevented late
subscriptions from receiving old events, but did not preserve old recipients.

## Decision

The `events` insert trigger captures the account's enabled source/type
subscription candidates, their app IDs, and their JSON filters in the outbox
row in the same transaction as the event. The scheduler evaluates each captured
filter against the event payload and enqueues deterministic invocations. A
duplicate event identity retains the first receipt and its candidate snapshot.
The database candidate predicate mirrors the bounded scheduler matcher,
including edge wildcard patterns. App deletion status is checked at acceptance.

A `NULL` snapshot marks a receipt accepted before this migration; those
receipts retain ADR-284's current-subscription routing. An empty array means
there were no eligible candidates at acceptance and is acknowledged without
reading current subscriptions. Snapshot and legacy receipts use the same
invocation identity, so recovery remains idempotent.

## Consequences

Subscription changes during an outage no longer alter new events' recipient
sets. The snapshot includes source/type candidates, including filters that may
not match the event data; the scheduler remains the authority for JSON filter
semantics. Publish transactions now read eligible subscriptions and write their
candidate data into the outbox, increasing transaction work and outbox size
with subscription fanout. App removal after acceptance may still prevent an
invocation from running; app lifecycle handling is a separate concern.
