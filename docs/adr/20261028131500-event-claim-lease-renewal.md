# ADR: Event routing claims last a minute and are renewed while held

Date: 2026-10-28
Status: Accepted
Amends: [ADR-647](647-independent-event-routing-default.md) (5-minute receipt and recipient claim leases)

## Context

A GA gap from the 2026-10-25 event audit. Receipt claims (adoption and
whole-receipt routing) and recipient claims set `lease_until` to 5 minutes
after the claim and never renewed it. A sweep holds up to 100 adopted
receipts and 20 recipients until it routes them, and releases them only on a
graceful stop. After a SIGKILL, an OOM kill or a node loss, those events
waited 5 minutes for their leases to lapse, and every ordered work-policy
lane behind one of them waited too: the ordering blocker treats a
`processing` prior as blocking.

Routing one claim is a short transaction, so a claim rarely needs 5 minutes;
a long lease only bounded how slow a sweep could be before another node took
its claims.

## Decision

1. **A minute per claim.** The four claim queries set `lease_until` to the
   claim time plus 1 minute (`state.PublishedEventLease`).
2. **Renewed while held.** While a sweep routes a claimed batch, a keeper
   renews the batch's unrouted claims every
   `state.PublishedEventLeaseRenewal` (20 s), matching each by its claim
   token, so a claim another worker has taken since stays theirs. Routing
   still fences on the token and generation, so a lapsed claim can never be
   admitted twice. The keeper stops before the sweep releases or moves on.
3. A failed renewal is logged and retried on the next tick; at worst the
   claim lapses and another worker routes it, as before.

## Consequences

- After a crash, claimed events and the ordered lanes behind them wait at
  most a minute instead of five.
- A sweep slowed by the database keeps its claims however long it takes.
- An older schedd still claims for 5 minutes and does not renew; both
  leases fence the same way, so the two releases run side by side.
- Renewals write only while a batch takes longer than 20 s.
