# ADR-1313: Stripe automatic tax on exported invoices

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** A Stripe billing connection can set `automatic_tax`. Invoices it exports are then created with `automatic_tax[enabled]=true`, so Stripe Tax adds the tax for each customer's location.
  - **Stripe only.** Paddle is the merchant of record and calculates tax itself. Lago calculates tax with its own tax settings. Other providers return 422.
  - **Fixed per export.** Each export records the setting it began with, like its collection mode (ADR-1311).
- **Why:** Collected invoices (ADR-1311) reach customers without an operator editing the draft, so the tax a seller owes has to be on them already. Operators selling in several countries use Stripe Tax for this.
- **Consequences:**
  - **Totals.** Gregale's statement totals stay net of tax, and the export's amount is the statement total. Stripe's invoice total adds the tax, so the payment status Gregale reads (ADR-1504) shows amounts including it.
  - **Stripe Tax setup.** The operator's Stripe account needs Stripe Tax set up, and customers need an address Stripe can tax. Otherwise Stripe rejects the invoice and the export fails with Stripe's message (422), to be fixed in Stripe and retried.
  - **Prices are tax-exclusive.** Invoice items are created without a tax behavior, so Stripe's default for the account applies. Operators who price tax-inclusive set that in Stripe.
- **Rejected alternatives:**
  - Computing tax in Gregale would need tax rates, registrations and filings that Stripe Tax already handles.
  - Per-export tax flags would let a billing run mix taxed and untaxed invoices for the same account.
