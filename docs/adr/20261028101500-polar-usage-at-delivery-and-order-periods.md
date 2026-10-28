# ADR 20261028101500 · Polar usage is stamped at delivery, and orders are read by their lines

- **Status:** accepted
- **Date:** 2026-10-28
- **Amends:** ADR-1162 (billing delivery finality, `1162-billing-delivery-finality-spend-cap-and-plan-history.md`); credit consumption (issue #279)
- **Related:** ADR-20261027093220 (object storage billed daily, already stamped at delivery)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A Polar sandbox run against `main` (2026-10-27) exercised checkout, a forced
renewal with metered usage, and a failed renewal. It found three defects.

1. **Usage before a meter reset is never billed.** meterd sends each final UTC
   hour of compute (and egress) overage as one Polar event stamped at the
   hour's start. A Polar meter counts only events stamped after its last reset,
   which happens when the subscription starts and at every renewal. The hour
   that contains a reset is delivered after it, but stamped before it, so it
   is dropped from both cycles. A renewal billed "0 consumed units" for 10 GB-h
   of delivered overage. A meterd backlog near a renewal widens the loss to the
   whole backlog (up to the 24-hour catch-up window).
2. **Every Polar invoice has an empty period, so credits never apply.** Polar
   puts the service period on each order line (`items[].start_timestamp` /
   `end_timestamp`), not on the order. The webhook parser read top-level fields
   that Polar does not send and fell back to the order's creation time for both
   ends. Credit consumption recomputes overage over that empty period, gets
   zero, and leaves the credit untouched even when the invoice billed usage.
3. **Unpaid renewals are invisible.** A declined renewal leaves a `pending`
   Polar order. apid stores an invoice for any `order.*` delivery, but the
   documented webhook subscription lists only `order.paid` and refunds, so a
   past-due customer's invoice history does not show what they owe.

## Decision

1. **Usage events are stamped when they are sent.** Compute and egress events
   carry the delivery time as their Polar timestamp, so they land in the cycle
   that is open when they arrive. The usage hour stays in `metadata.window_start`
   and in the external ID, which still deduplicates retries. Usage that crosses
   a renewal is billed on the next invoice instead of being lost. This matches
   object-storage charge steps (ADR-20261027093220).
2. **An order's period comes from its lines.** The recurring plan line spans
   the service period; a metered line can have start == end. Lines with a real
   span decide the period, then any line, then the subscription's current
   period, then the creation time. Webhook projection and invoice-history
   import share one parser.
3. **Credits offset the usage an invoice billed.** When an invoice has a
   complete, fully categorised line snapshot, the overage credits may offset is
   the sum of its `Usage` lines. Recomputing usage over the service period is
   only the fallback, because usage is billed in arrears and need not fall in
   the service period. The refund cap (the invoice's refundable amount) is
   unchanged, so a discounted invoice never refunds more than was paid.
4. **Unpaid orders are projected, and never reopen a paid invoice.** The Polar
   webhook endpoint must also subscribe to `order.created` and `order.updated`;
   a `pending` order is stored as an `open` invoice. Providers can deliver order
   events out of order, so an `open` or `draft` delivery never downgrades a
   stored `paid` invoice or its paid amount.

## Consequences

- Operators add `order.created` and `order.updated` to the Polar webhook
  endpoint (docs/ops/billing-provider-switch.md lists the full set).
- A Polar invoice's usage line covers usage delivered during the previous
  cycle, which is usually the previous cycle's usage shifted by about an hour.
- Invoices already stored with an empty period keep it until Polar redelivers
  them or the invoice-history import refreshes them.
- Rejected: stamping events at the hour start clamped to the subscription's
  current period start (needs a Polar read per delivery and still races the
  renewal); crediting against the recomputed period (wrong period for arrears
  usage).
