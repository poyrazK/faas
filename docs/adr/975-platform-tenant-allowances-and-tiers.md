# ADR-975: Allowances and tiers on platform tenant rate cards

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** A platform tenant rate card can carry `included_units_per_month` or a graduated `tiers` ladder, with the same validation as app cards (ADR-971, ADR-972). Both count the tenant's attributed usage across every app, in minute order through each UTC month. Ties within a minute are broken by subject, so pricing is deterministic.
  - **Calendar months.** While such a card is in force for any minute of a period, cross-app statements for that period must cover exactly one UTC calendar month.
  - **Re-pricing.** Each revision re-prices the whole month's usage. Its lines are the per-key difference from all finalized revisions of the month, keyed by subject, price source and unit price.
  - **App cards.** An app card with an allowance, tiers, route weights or a plan now blocks a cross-app statement only for minutes no tenant card prices, instead of always.
- **Why:** Tenant cards could only set a flat price, and any app card using the newer pricing features made the whole tenant unbillable across apps. Customers buying across several apps need one shared allowance or volume ladder.
- **Consequences:**
  - **One line per price.** A re-priced minute expands into one line per unit price: free allowance units at price 0 and each ladder step at its price. Every line stays exactly `billable_units × price_millicents_per_unit`, and per-subject units still equal the revision's minute coverage.
  - **Coverage is unchanged.** Coverage still records only the minute units not yet finalized, so the existing regression detection and handoff overlap rules apply as before.
  - **Negative adjustment lines.** In a re-priced adjustment (`Repriced` in state validation), a line can be negative when late usage moves free units or a cheaper step to an earlier minute. A subject can carry offsetting lines without new usage, and line windows need not match coverage windows. The revision total must be non-negative; otherwise the request returns 409, because Gregale never issues credits (as in ADR-972). Integrators that summed lines assuming positive values must handle negative lines in these adjustments. Flat-card statements are unaffected.
  - **Overlap with other periods.** If a finalized statement for a different period already billed minutes of the month, re-pricing would bill them twice. The draft returns 409, so tenants with monthly cards must be billed by calendar month.
  - **No backdating.** Once any tenant card has an allowance or tiers, new tenant cards cannot be backdated, since that would re-split units already billed.
  - **Minutes before the first tenant card** still use flat app cards. They do not consume the tenant allowance.
- **Rejected alternatives:**
  - Storing charged and step units per coverage minute, as app statements do (ADR-970), would change the private coverage format and the SQL that plans deltas. Diffing public lines reuses what each revision already stores.
  - Per-app allowances under a tenant would contradict a customer-wide price.
  - Tenant-level route weights need tenant-scoped route labels across apps, and tenant plans need tenant plan assignment. Both are left for later.
