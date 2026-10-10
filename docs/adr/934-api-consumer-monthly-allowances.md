# ADR-934: Monthly included units on app consumer rate cards

- **Status:** accepted
- **Date:** 2026-10-09
- **Decision:** An app rate card can set `included_units_per_month`: free request units per consumer per UTC calendar month while the card is effective. Platform tenant rate cards do not have allowances yet.
- **Why:** Every request was billed from the first one. "The first 10,000 requests a month are free" is the most common API pricing shape, and without it customers had to subtract credits by hand outside Gregale.
- **Consequences:**
  - **Consumption order.** A consumer's billable units in a UTC calendar month are counted in minute order from the start of the month. A minute's units are free while the month's earlier usage is below the allowance of the card effective at that minute, and charged afterwards. A card that takes effect mid-month counts the same monthly total, so raising the allowance mid-month frees units only up to the new total. The result depends only on the usage ledger, not on how statements split the month, so statements with different periods in one month agree.
  - **Quotes and statements.** Quotes and statements read usage from the start of the period's first month, so earlier usage consumes the allowance before the reported window. Buckets record `charged_units`, and the amount is charged units times the price. Buckets written before this change have no `charged_units` and read as fully charged.
  - **Adjustments compare charged units.** Late usage early in a month can exhaust the allowance sooner and turn later free units into charged ones without adding units to those minutes. An adjustment revision (ADR-933) therefore bills the difference in both units and charged units against finalized coverage. A minute's adjustment can charge units it does not add. Usage only grows, so charged units never shrink; a drop still fails closed with 409.
  - **No backdating.** Once any of an app's cards includes units, a new card cannot take effect in the past. A backdated card would re-split minutes that statements already billed, and could silently re-price finalized usage.
  - **Platform tenant statements.** A cross-app statement that would fall back to an app card with an allowance returns 422. The allowance is per consumer, but tenant statements mix sources and price only usage deltas. Customers bill those consumers through app statements or set a tenant rate card.
- **Rejected alternatives:**
  - An allowance per statement period was rejected because splitting a month into weekly statements would grant the allowance several times.
  - Tying the allowance to the card version, resetting whenever a new card takes effect, was rejected because a price change mid-month would hand out a second allowance.
  - Storing a running allowance balance was rejected because it is mutable state that late usage and retries must keep consistent; deriving the split from the immutable ledger needs no balance.
  - Graduated volume tiers are deferred. They can reuse the same minute-order consumption with several thresholds.

Migration headers retain the original billing-branch ADR number 844 to preserve published migration bytes; this decision is now numbered 934.
