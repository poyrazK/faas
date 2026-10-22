# ADR-20261019100330: Zero- and three-decimal statement currencies

- **Status:** accepted
- **Date:** 2026-10-19
- **Decision:** Statements, invoice exports and payment syncs support every ISO 4217 currency with at most three decimal places. Amounts stay in millicents (1/100,000 of the major unit, ADR-1306) everywhere they are stored and priced; only the edges convert.
  - **Minor units.** `pkg/api` keeps ISO 4217's exponent per currency: 0 for JPY, KRW, CLP, ISK, UGX and the rest of the zero-decimal list, 3 for BHD, IQD, JOD, KWD, LYD, OMR and TND, 2 otherwise. CLF and UYW (four decimals) are refused with the existing `invoice_currency_unsupported` problem. `amount_minor` fields, export amounts and payment amounts are in the currency's own minor unit: yen for JPY, fils for KWD.
  - **Rounding.** An exported invoice rounds each item half away from zero to the currency's smallest unit, and the rounding item (ADR-1502) keeps the invoice equal to the statement total rounded the same way. The credit line (ADR-1306) rounds the same way.
  - **Stripe's units.** Stripe has its own list. It takes its zero-decimal currencies, which include MGA, in whole units; ISK and UGX with two decimals that must be zero; BHD, JOD, KWD, OMR and TND with three decimals ending in 0; and every other currency, IQD and LYD included, with two decimals. Stripe exports rebuild the invoice in Stripe's unit, rounding each item to a multiple of its step, and convert Stripe's paid and remaining amounts back to ISO minor units for payment syncs.
  - **Paddle and Lago.** Paddle invoices only USD, EUR and GBP, unchanged. Lago takes the ISO representation.
  - **CLI.** `gregale` prints amounts with at least the currency's decimals (`¥1,200` has none) and parses `amount_minor` with the currency's exponent.
- **Why:** Rejecting every currency but two-decimal ones excluded Japanese, Korean and Gulf customers. Pricing in millicents already represents them; only the conversion to an invoice assumed cents.
- **Consequences:**
  - **Coarser Stripe units.** Where Stripe's unit is coarser than ISO's (the three-decimal currencies, MGA), a Stripe invoice can differ from the statement total by up to half of Stripe's unit, such as 5 fils, shown on its rounding item. Payment syncs report Stripe's amounts in ISO minor units.
  - **Existing exports** were all two-decimal, so their stored `amount_minor` keeps its meaning.
- **Rejected alternatives:**
  - Storing amounts in each currency's minor unit would change every pricing path and lose the sub-cent precision usage pricing needs.
  - Treating ISK and UGX as zero-decimal at Stripe sends amounts 100 times too small.
