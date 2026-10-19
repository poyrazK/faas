# ADR-20261019100331: Prepaid credit and spend caps for platform tenants

- **Status:** accepted
- **Date:** 2026-10-19
- **Decision:** A platform tenant (ADR-975) gets the prepaid credit and monthly spend cap ADR-1306 gave app consumers, across all of its apps.
  - **Credit grants.** `POST /v1/account/platform-tenants/{id}/credits` grants credit in one currency with an optional expiry and note; `GET` lists grants with the usable balance per currency; `DELETE …/credits/{credit_id}` voids what is left. Requests and limits are the consumer ones: at most 100 grants with credit left.
  - **Applied at finalize.** Finalizing a tenant statement draws usable grants in its currency down, oldest-expiring first, in the transaction that finalizes it, recorded in `platform_tenant_credit_applications`. The statement's `credit_applied_millicents` is the draw and `amount_due_millicents` the rest; both are on statement responses and the `platform_tenant.statement.finalized` webhook, which also gains the revision's `fees`.
  - **Exports and billing runs.** A tenant invoice export carries the credit as the same negative "Prepaid credit applied" line (one summary item for Paddle and Lago), and billing runs skip tenant statements that credit pays in full.
  - **Spend caps.** `PUT /v1/account/platform-tenants/{id}/spend-cap` sets a monthly cap in one currency. The consumer spend check worker also checks tenant caps every minute, and at once when a cap is set or credit changes. A tenant's amount due is its usage for the month to date, priced like a statement over every app (finalized revisions plus the unbilled delta), minus usable credit and credit already applied this month. Monthly fees are not counted, as for consumers. A zero cap allows prepaid usage only.
  - **Enforcement.** Tenant admission (ADR-1324's overdue check) refuses a tenant capped this month with the new scope `spend_cap`, and the gateway answers 402 `tenant_spend_cap_reached` with the cap as `limit` and the amount due as `observed`. The refused request is not billed. While API monetization is off (ADR-20261018090210) the gateway serves capped tenants and the worker pauses.
  - **Audit.** `platform_tenant_credit.granted`, `platform_tenant_credit.voided`, `platform_tenant.spend_cap_set`, `platform_tenant.spend_cap_removed`, `platform_tenant.spend_cap_reached` and `platform_tenant.spend_cap_cleared`.
- **Why:** ADR-1306 left tenants out, so a platform selling prepaid bundles to its own customers, or wanting to stop one customer's spend across several apps, had to do it by hand.
- **Consequences:**
  - **Soft cap.** As for consumers, a tenant can overshoot by about two minutes of traffic.
  - **Separate balances.** Tenant credit pays only tenant statements and consumer credit only consumer statements; a consumer linked to a tenant is billed by whichever statement the operator finalizes and exports.
  - **Currency.** A cap compares only with a month to date priced in its currency.
  - **Ownership.** apid writes grants, applications and checks; the gateway only reads the cap through admission.
- **Rejected alternatives:**
  - Summing the caps of the tenant's consumers would miss surface and JWT usage attributed to the tenant without a consumer.
  - Counting fees toward the cap would refuse a new tenant on the 1st for its base fee alone.
