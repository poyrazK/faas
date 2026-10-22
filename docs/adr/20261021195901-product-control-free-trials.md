# ADR-20261021195901: Product Control free trials

- **Status:** accepted for internal implementation
- **Date:** 2026-10-21
- **Amends:** ADR-20261021154511 (plan fees and included starts): trial time owes no fee, and trial starts use no included starts
- **Decision:** A Product Control plan can offer a free trial, as API consumers (ADR-1314) and platform tenants (ADR-1322) already can. During the trial, runs and the monthly fee cost nothing.
  - **Offering.** `trial_days` on a plan, 1 to 90.
  - **Starting.**
    - A customer's trial starts when they are first assigned to a plan with `trial_days` and have never had a trial in the app. Any assignment path starts it: owner assignment, reviewed owner change, self-service change or onboarding.
    - It starts with the assignment and lasts the plan's days, so a new customer's first fee minute is the trial's end.
    - A customer gets one trial per app. Moving to another plan during the trial keeps the trial's end. A customer whose trial has ended starts no other.
  - **Runs.**
    - A start admitted during the trial is priced at 0 and marked `trial` in its admission evidence.
    - It counts toward `monthly_starts`, which still limits the trial, but not toward `included_starts`: after the trial, the included starts are counted afresh, as ADR-1314 does for allowances.
    - When the customer has a trial, the quote binds the next start's price, so a confirmation made during the trial goes stale once the trial ends (409 `product_quote_changed`).
  - **Fees.**
    - A month's fee covers only the minutes outside the trial, from the customer's first assignment. It is the highest monthly fee among the plans held in those minutes, prorated by the share of the month they cover, rounded half up to the millicent (ADR-1325).
    - A month wholly inside the trial owes nothing.
    - The review's fee names the trial it excludes.
  - **Owner control.**
    - `PUT /v1/apps/{slug}/product/customers/{tenant}/trial` with `ends_at` moves a trial that has not ended, never earlier than the next UTC minute and never more than 90 days after it started.
    - `DELETE` ends it at the next UTC minute.
    - A customer without a running trial is refused with 409 `product_trial_unavailable`.
    - Changes are audited as `product.customer.trial_changed`.
    - `gregale product customers trial` does the same.
  - **Release gate.** `trial_days` and the trial routes need `product_control_plan_pricing_enabled` (ADR-20261021154511). A previous apid would ignore the trial: it would charge its runs and its fee.
- **Why:** An owner selling a paid plan needs customers to try it first. Product Control could only offer that with a free plan the owner had to swap by hand, which also changes the plan's prices and allowances for every customer on it.
- **Consequences:**
  - **Never retroactive.** A trial starts with its assignment and moves or ends only at a later minute, so a run already admitted keeps its price and a finalized statement stays exact.
  - **Trial on the customer, not the plan.** Trial dates are kept on the customer's assignment. The plan only says how long a new trial lasts, so publishing a catalog never changes a running trial.
  - **Fees across plans.** A customer who moves from a paid plan to a trial of a dearer one pays the dearer fee for the minutes outside the trial, the highest-fee rule ADR-20261021154511 sets.
- **Not in this decision:** card collection before the trial ends (self-serve checkout) and trial reminders by email. Both depend on self-serve signup.
- **Rejected alternatives:**
  - **Trials owner-granted only** (ADR-1314's choice) would leave the plan without a stated trial for self-serve signup to offer.
  - **Waiving a month that starts during the trial** (ADR-1322's fee rule) would give a 14-day trial ending on the 2nd almost two free months.
  - **Counting trial starts toward included starts** would leave a customer who tried the product heavily without the plan's included starts in their first paid month.

## Implementation slices

1. Plan `trial_days`, trial start on assignment, free trial runs, fees outside the trial, quote binding, portal and console display.
2. Owner extend and end: routes, CLI and audit.
