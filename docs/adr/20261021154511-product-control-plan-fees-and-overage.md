# ADR-20261021154511: Product Control plan fees, included starts and overage

- **Status:** accepted for internal implementation
- **Date:** 2026-10-21
- **Amends:** ADR-1701 (one price per verified success), ADR-1709 ("no recurring subscription charge"), ADR-1704 (statements carry only run lines)
- **Decision:** A Product Control plan can charge a monthly fee, and a feature grant can include a number of starts in it, with later starts charged per success up to the plan's monthly limit. This is the common "€29 a month, 100 runs included, then €0.20 each, up to 1,000" plan. The pricing rules are those API monetization already uses for fees (ADR-1316, ADR-1325), applied to Product Control's starts.
  - **Included starts.** `included_starts` on a feature grant, from 0 to its `monthly_starts`.
    - The first `included_starts` starts of the feature in the customer's UTC month are admitted at price 0. Later starts are admitted at `unit_price_millicents`.
    - Starts count as they do for the monthly limit: every admitted start, across plans and catalog revisions, whether it later succeeds, fails or is cancelled.
    - `monthly_starts` stays the hard limit, so it also caps the overage.
    - The default, 0, keeps today's pricing.
  - **Price at admission.** The receipt stores the price of its own start, 0 or the unit price, as it stores the unit price today. Usage, statements, credits, explanations, run details and invoices therefore keep summing receipt prices.
    - A credit is still capped at its run's price, so an included run has nothing to credit.
    - Statements and invoices show included runs as their own no-charge line, "included successful … runs", as runs of a free feature already appear.
    - A statement that charges nothing is not exported. Billing runs already skip such statements.
  - **Consent.** When a grant includes starts, the quote also binds the price of the customer's next start.
    - If another start uses the last included start first, the confirmation fails with 409 `product_quote_changed`, and the customer is shown the new price. Nothing is charged at a price the customer did not see.
    - A grant without included starts adds nothing to the quote, so its quotes and catalog revisions hash as before.
  - **Monthly fee.** `monthly_fee_millicents` on a plan.
    - Each UTC month in which a customer holds a plan with a fee, the customer's statement charges the highest monthly fee among the plans (catalog revision and plan) they held that month, once.
    - In the month of the customer's first assignment, that fee is prorated by the minute from the assignment to the month's end, rounded half up to the millicent (ADR-1325).
    - Suspension does not stop the fee. An owner who stops charging moves the customer to a plan without one.
  - **Plan history.** Every assignment change is appended to `product_control_plan_history` in the same transaction that changes the customer. This covers owner assignment, the reviewed owner change and the self-service change. A month's plans are the one in force when it starts plus those assigned during it. The review of a plan change shows this month's fee and the new plan's.
  - **Statements.** A billing review carries `fees` (plan, revision, monthly fee, prorated share, amount), and `amount_millicents` includes them.
    - The review hash binds the fees, and finalization snapshots them.
    - A customer whose month has a fee but no runs gets a statement, and billing runs (ADR-20261018212918 §4) bill such customers too.
    - Each fee becomes its own invoice item: "Monthly fee: plan …", prorated when partial.
    - Credit notes still credit runs only. A fee is credited in the owner's billing provider.
  - **Release gate.** The hot runtime setting `product_control_plan_pricing_enabled` defaults to false.
    - While it is off, publishing a catalog that sets a plan fee or included starts is refused with 409 `product_plan_pricing_unavailable`.
    - Operators enable it only once every apid replica runs this release. An apid from the previous release ignores these fields: it would charge included starts at the full price and finalize statements without fees.
    - Turning the setting off later keeps published catalogs and their fees.
- **Why:** Product Control could sell only a price per successful run with a hard monthly limit. A customer who reached the limit was refused even when willing to pay, and an owner could not charge for access ("€29 a month") at all. Most products owners sell combine a subscription with included usage and paid overage, and API monetization already prices exactly that.
- **Consequences:**
  - **Starts, not successes, are included.** A start's price must be fixed and confirmed before it runs, and its outcome is unknown then. A failed or cancelled run uses an included start as it already uses a monthly start.
  - **Highest fee, not prorated changes.** Prorating a mid-month upgrade would sell the higher plan's whole monthly allowance for a fraction of its fee. A downgrade lowers the allowance at once and the fee from the next month, and the plan-change review says so.
  - **Included starts are not prorated** in the first month. An owner who wants to limit a late joiner's allowance sets `monthly_starts`.
  - **Spend.** `monthly_starts` × unit price plus the fee bounds a customer's month. Separate spend caps are later work.
  - **Separate ledger.** Platform tenant cards (ADR-1322) keep their own fees, and the same work must still not be priced in both ledgers.
- **Not in this decision:** per-unit (quantity) pricing reported by the Operation, free trials, discounts and annual plans. Each changes what a receipt or a statement means and needs its own ADR.
- **Rejected alternatives:**
  - **Counting included successes** would leave a start's price unknown at admission, so customers could not confirm it.
  - **Prorating every plan change by the minute** (ADR-20261019100332) would make an upgrade on the 30th buy a month's allowance for a day's fee.
  - **Charging the plan held when the month starts** (ADR-1316's original rule) would let a customer upgrade on the 2nd and pay the old fee for a month of the higher allowance.
  - **A separate overage price** beside the unit price would add a third price where "included, then the unit price" already expresses overage.
  - **Storing fees in a table of their own** without the plan history would not show which plans a month held when fees change between revisions.

## Implementation slices

1. Included starts and overage: catalog field and validation, admission price, quote binding, receipt evidence, owner, portal and CLI display, statement and invoice lines, release gate.
2. Monthly fees: catalog field, plan history, review and statement fees, invoice fee items, billing runs, plan-change review.
