# ADR-1312: Finalized volume months keep their step

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Once a month priced by a volume ladder (ADR-1309) has a finalized revision, late usage can no longer move it into a step whose total is lower.
  - **Locked step.** When exact re-rating would lower what finalized revisions charged, the adjustment prices each volume card at the step those revisions billed: the highest step with net positive units. For app cards, these are their buckets' `tier_units`; for tenant cards, their lines at that step's price.
  - **Only when needed.** Late usage that keeps the month in its step, or moves it to a step with a higher total, is re-rated exactly as before. The lock applies only when re-rating would otherwise return 409.
  - **Result.** The adjustment bills the late units at the billed step's price and changes no earlier minute. It is never negative.
- **Why:** ADR-1309 returned 409 for these adjustments, which left late units unbilled for good. Volume discounts make that common: a month finalized just under a boundary loses every late unit that crosses it.
- **Consequences:**
  - **Charged above the ladder.** Customers pay the billed step's price for late units, not the cheaper step the month's final total reaches, because Gregale issues no credits. Units arriving before the first finalization still price exactly.
  - **Quotes differ.** Quotes, spend caps and self-service views still price the month by its total, so they can show less than finalized revisions billed for a month with late usage.
  - **Other cards unchanged.** A graduated card whose re-rating would lower a total (a mid-month card change with unusual prices, ADR-951) still returns 409.
- **Rejected alternatives:**
  - Issuing credit notes would make Gregale move money back, which ADR-1502 rules out.
  - Locking the step at the first finalization even when the month later grows into a step with a higher total would undercharge growth the ladder prices.
