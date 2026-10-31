# ADR-20261022180921: Product Control trial-to-paid conversion

- **Status:** accepted for internal implementation
- **Date:** 2026-10-22
- **Amends:**
  - ADR-20261021195901 (free trials): a trial can require a payment method to keep its plan.
  - ADR-20261022100735 (self-serve signup and card on file): the saved card is recorded on the customer.
- **Decision:** A plan with a trial can require a payment method on file to keep the plan after the trial. A customer who has none when the trial ends moves to a fallback plan that costs nothing, effective at the trial's end.
  - **Offering.**
    - On a plan, `requires_payment_method: true` with `fallback_plan` naming another plan of the same catalog.
    - The plan must offer a trial (`trial_days`).
    - The fallback plan must have no monthly fee, no trial and no payment method requirement.
    - A catalog whose signup provider is Paddle cannot offer such a plan at signup, because a Paddle customer cannot keep a card on file (ADR-20261022100735).
    - The fields need `product_control_plan_pricing_enabled`, as trials do. A previous apid would ignore them and keep every customer on the paid plan.
  - **Payment method on file.**
    - The customer's assignment records `payment_method_at`, when a payment method was last seen on file. Gregale still stores no card data.
    - Completing the portal's card Checkout records it.
    - Reading the workspace's payment method refreshes it: it is recorded when Stripe has a default card and cleared when Stripe has none.
  - **Trial end.**
    - A background step resolves each ended trial once and records `trial_resolved_at`.
    - A customer whose plan, in their own catalog revision, requires a payment method, and who has none on file, moves to the fallback plan in that revision.
    - That move is appended to the plan history effective at the trial's end, not when the step runs. The month's fee therefore never charges the paid plan for minutes after the trial (ADR-20261021154511).
    - It is skipped when the customer's plan changed after the trial ended, because that later assignment was an explicit choice.
    - Every other customer keeps their plan.
    - Moves are audited as `product.customer.trial_fell_back`, kept plans as `product.customer.trial_converted`.
  - **Until resolved.**
    - Between the trial's end and its resolution, a customer on such a plan without a payment method is refused new starts with 402 `product_payment_method_required`.
    - Saving a card lifts the refusal at once, and resolution then keeps the plan.
    - Work already admitted keeps running.
  - **Self-service.**
    - A self-service change to a plan requiring a payment method is refused with 409 `product_payment_method_required` when the customer has none on file and would not be in a trial after the change: neither one running nor one the change starts.
    - Owner assignments are not checked.
  - **Portal.**
    - During such a trial, the portal says when the trial ends, that a card keeps the plan, and which plan follows otherwise.
    - A billing manager is offered the card page; other members are told to ask one. A workspace not billed through Stripe is told to contact the product team.
    - After the trial, a held customer sees the refusal's reason and the same action.
  - **Reminders (slice 2).**
    - Members who can manage billing get workspace updates 3 days and 1 day before such a trial ends, and one when it resolves.
    - With their consent, they also get them by email through the existing pipeline (ADR-1717, ADR-1720) as a new "billing notices" category.
- **Why:**
  - Trials without a card turn into unpaid usage or an invoice the customer never agreed to pay.
  - Owners need the trial to end in either a paying customer or a free one, without watching each trial by hand.
- **Consequences:**
  - **Bounded backdating.** The fallback takes effect at the trial's end, while the step runs up to one interval later. That interval admits no start for the affected customer, so no admitted run is repriced. A month cannot finalize inside it.
  - **Resolution is once.**
    - A card removed after resolution does not move the customer: they are paying, and their invoices fail into overdue holds (ADR-20261018212918) like any other.
    - The record is refreshed for later self-service changes.
  - **Paddle and Lago customers** placed on such a plan by the owner always fall back, since no payment method is recorded for them.
  - **Hashes.** Both new customer fields are omitted when unset, so earlier previews and receipts hash as before.
- **Not in this decision:**
  - collecting the card inside the trial-end email;
  - grace periods after the trial;
  - charging at trial end;
  - Paddle or Lago payment methods.
- **Rejected alternatives:**
  - **Holding the customer on the paid plan until they add a card.** The plan's monthly fee would keep accruing for a customer who never converted.
  - **Ending the trial lazily at admission.** The fee and the self-service options read the plan history, so the move must be recorded, not inferred.

## Implementation slices

1. Plan fields and validation; `payment_method_at` and `trial_resolved_at`; the resolution step; the 402 refusal; the self-service guard; recording the card; the portal banner.
2. Trial reminder updates and the "billing notices" email category.
