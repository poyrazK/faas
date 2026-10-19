# ADR-1324: Block platform tenants with overdue invoices

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** An account can refuse platform tenants whose exported invoice is unpaid a number of days after its due date, as an app can refuse its consumers (ADR-1315).
  - **Policy.** One rule per account, `block_overdue_after_days` from 0 to 365: `GET`/`PUT`/`DELETE /v1/account/platform-tenant-billing-policy`.
  - **Holds.** Every 5 minutes apid places a hold on each tenant whose exported tenant invoice is open or uncollectible with an amount left at least that many days past its due date, and lifts holds that no longer apply. A held invoice's payment status is read within 15 minutes, so paying lifts the hold within about 20. `GET /v1/account/platform-tenant-overdue-holds` lists them.
  - **Gateway.** The tenant admission check (ADR-240) reads the hold before the tenant's request budget, so a held tenant's consumers, surfaces and JWT rules all get `402 tenant_invoice_overdue` without consuming budget or recording billable usage.
- **Why:** A platform's customers are businesses with net terms; the operator needs the same lever for them that ADR-1315 gives for API consumers.
- **Consequences:**
  - **Per request.** The hold costs one indexed read in the admission transaction every tenant request already runs.
  - **App rules are separate.** A tenant-managed consumer's app rule (ADR-1315) still applies to that consumer's own invoices; a tenant hold applies to everything the tenant does.
- **Rejected alternatives:**
  - A per-tenant rule would be set once per customer and drift; operators set terms per account.
