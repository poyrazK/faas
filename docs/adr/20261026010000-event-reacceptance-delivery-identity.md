# ADR: A re-accepted event gets its own delivery identity

Date: 2026-10-26
Status: Accepted
Amends: [ADR-613](613-atomic-event-routing-handoff.md) (delivery identity)
Related: [ADR 20261023215834](20261023215834-event-plan-caps.md) (settled-receipt windows)

## Context

An event's identity is (account, source, id). Its receipt deduplicates a
repeat while the receipt is retained, the plan's settled-receipt window
(1/3/7/14 days on Free/Hobby/Pro/Scale). After the receipt is pruned the
documented contract treats a repeat as a new event, with the same or changed
content.

Routing gives each recipient a deterministic delivery,
`PublishedEventInvocationID(account, source, id, subscription)`, so recovery
after an uncertain commit finds the delivery it already made. Deliveries
outlive receipts: completed invocations are kept 1/7/30/90 days and failed or
dead-lettered ones at least 30. A repeat accepted after its receipt was
pruned therefore found the earlier delivery under the same identity,
admission refused it as a conflict (`created_at` before the receipt), and the
scheduler retried it to its attempt limit before marking the recipient
failed. Every stable-id re-send in that gap was lost: inbound webhook retries,
natural-key ids, a producer's weekly report.

## Decision

1. **Same identity, earlier acceptance, new delivery.** When admission finds
   the original delivery (or its work cancellation) and it predates the
   receipt being routed, the receipt routes to
   `PublishedEventReacceptanceID(account, source, id, subscription, outbox
   id)`. Admission then repeats its checks for that identity, so finding it
   means an earlier attempt of the same receipt committed, as before.
2. **The receipt records it.** The recipient's progress stores the identity it
   routed to (`delivery_id`, only when it is not the original). The receipt
   view, its replay history and its attempt history read it; without it they
   use the original identity, as before.
3. **The first acceptance is unchanged.** `PublishedEventInvocationID` keeps
   its encoding, and receipts that never collided record nothing.

## Consequences

- A repeat after the receipt window is delivered again, as the docs promise.
  Within the window the receipt still deduplicates it.
- The original identity no longer names every delivery of an identity.
  Execution lineage repair, execution recovery and consumer execution health
  still compute the original identity, so they report a re-accepted delivery
  as missing until they read `delivery_id` too (follow-up).
- Release N-1 beside N: an older schedd still refuses the re-acceptance (no
  new hazard); an older apid shows the re-accepted delivery as missing from
  the receipt. Older readers ignore the new JSON field.
- Legacy whole-event routing for object notifications and receipts without a
  recipient snapshot keeps the original identity.

## Alternatives

- **Treat the repeat as a duplicate of the retained delivery.** Contradicts
  the documented window and drops changed content entirely.
- **Salt every delivery with its receipt.** Changes the identity of every new
  delivery and every reader at once, with N-1 readers unable to find any.
- **Keep receipts while their deliveries are retained.** Receipts count
  against event storage; a long invocation retention would hold storage for
  events that are long settled.
