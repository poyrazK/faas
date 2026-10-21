# ADR-1325: Prorated monthly fees in the first month

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** An app or tenant card can set `prorate_monthly_fees`. Its base fee and minimum (ADR-1316, ADR-1322) for the month a consumer or tenant is created are scaled to the share of the month left after the creation.
  - **Share.** Whole minutes from the fee instant (the creation, truncated to the minute) to the month's end, over the month's minutes. The scaled amount rounds half up to the millicent.
  - **Which card.** The card in force at the fee instant decides, as for the fees themselves.
  - **Default.** Cards keep charging whole months unless they set the flag.
- **Why:** ADR-1316 charged a consumer created on the 30th a full month's base fee, which operators had to refund by hand or avoid with a trial.
- **Consequences:**
  - **Only the first month.** Revoking a consumer mid-month still bills the whole month. Prorating at the end would let a later revocation shrink a minimum already finalized, so an adjustment revision could fall below zero.
  - **Exports.** A prorated fee is one invoice item with the scaled amount.
- **Rejected alternatives:**
  - Prorating by day would overcharge creations late in a day and differ from the minute grid every other price uses.
