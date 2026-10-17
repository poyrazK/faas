# ADR-1309: Volume rate card tiers

- **Status:** accepted
- **Date:** 2026-10-14
- **Decision:** A rate card's ladder has a `tier_mode`: `graduated` (the default, ADR-951) or `volume`.
  - **Volume pricing.** Every unit of a UTC month costs the price of the step the month's total reaches: the first step whose `up_to` the total does not exceed, else the unbounded last step.
  - **Where it applies.** App cards, platform tenant cards and tenant plans take the mode. A volume card needs tiers.
  - **Ladder shape.** Volume ladders are validated like graduated ones, so only the first step may be free. A month that stays in that step is free; once it passes it, every unit is charged.
  - **Month total.** The total counts every unit of the month, including minutes another card priced, as graduated positions do.
  - **Statement buckets.** A volume minute records all its units in one step of `tier_units`. Statements, exports and quotes need no other change.
  - **Quotes.** A quote within a month prices at the step that month's usage has reached so far.
- **Why:** Volume pricing ("all units at the rate of the tier you reach") is the other common API price shape. ADR-951 rejected it because crossing a boundary re-prices every earlier minute. ADR-951's exact re-rating and ADR-975's whole-month re-pricing now handle that: a revision carries the per-minute differences, and statements of tiered cards already cover whole months.
- **Consequences:**
  - **Late usage across a boundary.** After a month is finalized, late usage that moves it into a cheaper step would lower the total, and Gregale never issues credits. ADR-1312 keeps the billed step for such adjustments; late usage within the billed step, or into a step with a higher total, bills normally.
  - **Cliffs are the seller's choice.** A volume ladder can make a month's total fall as usage grows (for example, 1,000 units at 10 cost more than 1,001 at 9). Gregale prices exactly what the ladder says and does not smooth it.
  - **Spend caps.** Spend caps (ADR-1306) re-price the month to date every minute. A month whose total falls when it crosses a boundary therefore lifts a cap at the next check.
- **Rejected alternatives:**
  - Allowing only ladders whose total never falls at a boundary would rule out ordinary volume discounts.
