# ADR-20261104212028: Account credit is a discount on the next order, not a refund

- **Status:** accepted (owner, 2026-11-05: credits "Whole order", "Carry forward", on the billing GA questions; 2026-11-04: "Polar discounts first")
- **Date:** 2026-11-05
- **Owner:** apid (billing) maintainers
- **Amends:**
  - spec §4.7.1 (operator credits): credit is no longer drawn against a paid invoice's usage overage.
  - [ADR-220](220-provider-scoped-credit-receipts.md): the provider-scoped receipt now keys a credit discount's draw on its order; no refund is coupled to it.

## Context

Operators issue account credit with `POST /v1/admin/accounts/{id}/credits`.
Until now, apid drew it when a paid invoice's webhook arrived, up to the usage
the invoice billed, and refunded that amount to the card
(`consumeAndRefundInvoiceCredits`). The customer was charged first and saw a
refund days later. Tax was charged on the full amount and refunded in
proportion. A failed payment never used the credit, and it applied only to
usage overage, never to the plan fee.

A Polar sandbox probe (2026-10-27) showed that a fixed `once` discount on a
subscription reduces its whole next order, metered line included, before tax.
A subscription carries one discount, set with `PATCH /v1/subscriptions/{id}`
`{discount_id}`, and it applies from the next billing cycle. A discount cannot
target only the metered price: the plan fee and the usage are one product.

## Decision

1. **Credit is a one-time fixed discount on the subscription's next order.**
   - apid creates a Polar discount: `type: fixed`, `duration: once`,
     `max_redemptions: 1`, no code (API-only), named "Gregale credit", with
     the subject in its metadata. It sets it as the subscription's
     `discount_id`.
   - The amount is the subject's credit still active when the subscription
     renews (`current_period_end`). A credit that expires before then is left
     out.
   - Credit covers the whole order before tax: the plan fee, usage, object
     storage and managed PostgreSQL lines. This replaces the "usage overage
     only" rule.
2. **apid records each discount before attaching it.**
   - `credit_discounts` keeps the subject, the provider subscription and
     discount IDs, the amount and the state: attached, redeemed or detached.
   - The row is written after Polar creates the discount and before it is put
     on the subscription. So an order can never carry a credit discount apid
     does not know about, which would leave the credit undrawn. A failed
     attach detaches the row and deletes the discount.
3. **An order draws what Polar applied, once.**
   - Every Polar order webhook reads the order's `discount_id` and
     `discount_amount`. When the discount is a credit discount, apid draws
     `min(discount_amount, discount amount)` from the subject's credits, FIFO,
     after storing the invoice projection and before claiming the delivery.
   - `credit_ledger`'s receipt per provider order (ADR-220) makes a
     redelivery draw nothing more. A failure answers 503 so Polar retries.
   - The draw happens whether or not the order is paid, because the order
     was reduced when it was created. Audit row `credit.applied` carries the
     order's invoice, the discount and the amounts.
   - If the credit left is smaller than the discount (it expired or was spent
     after attaching), apid draws what is left and logs it.
4. **apid keeps the discount in step with the credit.**
   - A sync reads the subscription, the subject's active credit and
     `credit_discounts`. It attaches a new discount when the amount changed,
     detaches when no credit is left, and otherwise keeps the discount.
   - Replaced discounts are marked detached and deleted at Polar. One already
     applied to an order is still drawn when that order's webhook arrives.
   - Syncs run:
     - after an operator issues credit;
     - after an order draws credit;
     - for every subject with credit or an attached discount, at the end of
       each billing reconcile pass (every 30 minutes, under its lock).
   - One subject is synced at a time per replica. Replicas that race leave
     the subscription with one discount, and the next sync settles it.
5. **Credit that cannot be attached carries forward.**
   - Polar refuses changes while a cancellation or plan change is scheduled,
     and apid does not replace a discount it did not create. In both cases
     the credit stays on the account.
   - A subject without a subscription (Free, or before checkout) keeps its
     credit until it subscribes.
   - Credit is never refunded with Polar: it has no cash value.
6. **The refund path is off for Polar.**
   - Paid-invoice webhooks no longer draw credit with Polar.
   - `POST /v1/invoices/{id}/consume-credits` answers 409: credit is applied
     as a discount, not drawn against a paid invoice.
   - Providers without discount support (the Stripe and Paddle compatibility
     adapters) keep the previous refund behaviour.

## Consequences

- Customers never see a charge and refund for credit, tax is computed on the
  reduced order, and credit also pays the plan fee.
- An upgrade's prorated order may use the attached discount (unverified in
  the sandbox). If it does, its webhook draws the credit the same way, and
  the next sync attaches a new discount for the rest.
- Credit issued in the last 30 minutes before a renewal may miss that order
  if the post-issuance sync fails, and then applies to the following one.
- The first order of a new subscription is not discounted. Checkout does not
  carry a credit discount, so credit applies from the first renewal.
- Release order: an apid N-1 replica still refunds credit on a paid invoice.
  Both paths draw under the same per-order ledger key, so the balance is
  drawn once. But an N-1 replica could refund an order that a discount
  already reduced, and the customer would get both. Roll every apid replica
  before issuing credit to a Polar subject. Production billing is not live
  yet.
- Verification: needs a sandbox run with a card checkout: attach, renewal
  with the discount, draw, replace and detach, and an upgrade's prorated
  order.
- Rejected:
  - **Usage-only credit sized before renewal.** The discount would be sized
    from usage billed so far, a few hours before renewal. Usage after that
    would still be charged, and the timing job adds failure modes.
  - **A refund fallback when no discount can be attached.** It keeps two money
    paths and card-statement refunds alive for rare cases. The owner chose to
    carry the credit forward.
