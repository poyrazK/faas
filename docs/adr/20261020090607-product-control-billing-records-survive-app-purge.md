# ADR-20261020090607: Product Control billing records survive app purge

- **Status:** accepted
- **Date:** 2026-10-20
- **Related:** ADR-1162 (usage survives app purge), ADR-1704 (Product Control billing, immutable statements and handoffs), ADR-022 (7-day app tombstone), ADR-021 (staged account deletion), ADR-566 (financial history is erased with the account)
- **Decision:** An app's Product Control receipts, waivers, finalized statements and credit notes, invoice handoffs and catalog revisions are kept when the app is purged. They are erased only with the owning account.
  - `product_control_receipts`, `product_control_billing_documents` and `product_control_revisions` lose their `app_id` foreign keys to `apps`. Their account and tenant foreign keys stay `ON DELETE CASCADE`, so the account's final deletion still erases them (ADR-566).
  - Waivers and invoice handoffs keep cascading from receipts and documents, so they stay with them.
  - Revisions stay because receipts reference the revision they were admitted under. Dropping revisions with the app would make the purge fail on that reference.
  - Configuration and customer data still go with the app: heads, customer assignments, plan and customer changes, onboarding, help, notifications and email.
- **Why:** `DeleteAppPermanently` deletes the app row 7 days after an app delete (ADR-022), and every Product Control table cascaded from it. That erased finalized statements, the external invoice references recorded against them, and the receipts that prove each charge, while the account that issued the invoices still existed. An exported statement's provider invoice (ADR-20261018212918) would also point at a statement that no longer existed. ADR-1162 already keeps usage billing evidence across the purge for the same reason.
- **Consequences:**
  - **Records outlive their app's routes.** A purged app's slug no longer resolves, so the Product Control billing routes cannot read its records; they stay in the database for the account and for support, keyed by the old app ID. A later app that reuses the slug has a new ID and never sees them. Reading retained records of a purged app is a follow-up.
  - **No orphan reads.** Every Product Control billing read and write already requires a live app and a published catalog (`ProductBillingScopeExists`), so retained rows are never served through a new app.
  - **Account deletion is unchanged.** Personal accounts still erase all their Product Control data after the 30-day grace; shared apps still block erasure until their ownership is reviewed (ADR-202610171904).
  - **API monetization statements are not covered.** `api_consumer_usage_statements` and their handoffs still cascade from the app. The same retention question applies to them and is left to a separate decision.
- **Rejected alternatives:**
  - **Refusing to delete an app that has billing records** would block an owner from removing an app they no longer sell, with no way to resolve it short of deleting the account.
  - **`ON DELETE RESTRICT` on the app foreign keys** has the same effect: the purge would fail every minute and the app would never be removed.
  - **Copying records into an archive table before the purge** duplicates immutable rows and needs a second read path, for no benefit over keeping them in place.
