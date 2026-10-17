# ADR-1322: Monthly fees and free trials for platform tenants

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** A platform tenant card or plan can charge a monthly base fee and a minimum spend, and a tenant can have a free trial, with the rules ADR-1316 and ADR-1314 set for app consumers.
  - **Fees.** `base_fee_millicents_per_month` and `minimum_millicents_per_month` on a tenant card (and the plan that creates it). Each unit stream's card in force when the tenant's month starts (00:00 UTC on the 1st, or the tenant's creation if later) charges its own base fee once and tops its stream's usage charge for the month up to its minimum. Late usage lowers a minimum already billed; a revision's total never falls below zero.
  - **Statements.** Tenant statements carry `fees`, and `amount_millicents` includes them. A tenant without new usage gets a revision with fees and no lines or coverage. Periods a fee card prices must be whole UTC months. Finalized-statement webhooks carry the fees.
  - **Billing runs.** Runs (ADR-1301) bill tenants created before the month ends whose cards charge fees, with or without usage. Tenants are billed whether active or suspended.
  - **Trials.** `GET`/`PUT`/`DELETE /v1/account/platform-tenants/{id}/trial`. A tenant gets one trial, starting at the next minute, extendable or ended early, never backdated. Its usage in the trial is priced at zero on both the per-minute and whole-month paths and consumes no included units; a month starting during it, or the minute after, has no base fee or minimum.
  - **Exports.** Fee lines become their own invoice items, as for app statements (ADR-1316).
- **Why:** Platform operators sell subscriptions to businesses ("€499 a month including a million requests") and offer trials to win them; tenants could only be billed for usage.
- **Consequences:**
  - **No proration by default.** A tenant created mid-month pays the whole month's fee and minimum unless the card prorates them (ADR-1325).
  - **Spend caps** (ADR-1306) remain app-consumer controls.
- **Rejected alternatives:**
  - Billing fees as a separate statement would need a second invoice per month and break the one-revision-per-handoff rule.
