# ADR-20261019100332: Prorated monthly fees across card changes and revocation

- **Status:** accepted
- **Date:** 2026-10-19
- **Decision:** A card with `prorate_monthly_fees` (ADR-1325) charges its base fee and minimum for the share of the month it is in force, not only in the first month.
  - **Segments.** Within each price stream (ADR-1317) the cards are ordered by `effective_from`. The card in force at the fee instant charges first: a card that does not prorate charges the whole month as before (ADR-1316); a prorating one charges until the next card takes over. A later card in the month charges only if it prorates, from its `effective_from` until the next card or the month's end. A plan change (ADR-949) is a new card, so a consumer or tenant moved between prorating plans mid-month pays each plan's share.
  - **Minimums.** A prorating card's scaled minimum is covered by the usage that card priced; a non-prorating card's by its whole stream, as before.
  - **Revocation.** A consumer revoked mid-month pays prorating cards' shares until the revocation, unless a revision of that month was finalized before it. Finalized fees are never reduced, so an adjustment revision never falls below zero. Billing runs finalize monthly statements after the month ends, so they always prorate.
  - **Share.** Whole minutes in the segment over the month's minutes, rounded half up to the millicent, as ADR-1325.
- **Why:** ADR-1325 prorated only the first month. Moving a customer to a cheaper plan on the 20th charged both plans' full fees or only the old one's, and a revoked customer paid its whole last month, which operators refunded by hand.
- **Consequences:**
  - **Mixed cards.** Moving from a card that does not prorate to one that does charges the old card's whole month and the new card's share; moving to a card that does not prorate starts charging next month. Operators who want exact splits use prorating cards on both sides.
  - **Late finalize.** Finalizing a month before a revocation keeps that month's full fees.
  - **Tenants** have no revocation, so only card changes split their fees.
- **Rejected alternatives:**
  - Crediting back finalized fees after a revocation would need negative revisions, which statements do not allow (ADR-1325).
  - Splitting non-prorating cards too would change bills of every existing card without its operator opting in.
