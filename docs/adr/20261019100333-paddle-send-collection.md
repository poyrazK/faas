# ADR-20261019100333: Paddle invoices sent by Paddle

- **Status:** accepted; amended by ADR-20261020120000 amendment 1 (the address is the newest created, not the most recently updated)
- **Date:** 2026-10-19
- **Decision:** A Paddle billing connection (ADR-1507) can set `collection: send`. Paddle then emails the exported invoice to the customer and collects it, as `send` does for Stripe (ADR-1311).
  - **Billing.** The export still creates a manually collected draft transaction, now with the connection's `days_until_due` as payment terms. It then reads the customer's most recently updated active address and patches the transaction to `billed` with it, after which Paddle numbers the invoice and emails it.
  - **No address.** Paddle cannot bill a customer without an address. The export fails with 422 "Paddle customer has no address" and keeps the draft; exporting again once the address exists bills the same transaction, as any resumed export does.
  - **Key.** The key needs read access to customers besides transaction access.
  - **Charge.** `collection: charge` stays refused for Paddle: Paddle cannot charge a saved payment method for a manually collected invoice.
- **Why:** Without sending, every Paddle invoice had to be opened and billed by hand in Paddle's dashboard.
- **Consequences:**
  - **Payment syncs** (ADR-1504) already read billed and paid transactions, so sent invoices report open, overdue and paid without changes.
  - **Migration.** The connection's collection check allows `send` for Paddle. Rolling it back fails while a Paddle connection uses `send`; switch those to `none` first.
- **Rejected alternatives:**
  - Creating transactions as `billed` directly needs the address before the draft exists and loses the resumable draft step.
