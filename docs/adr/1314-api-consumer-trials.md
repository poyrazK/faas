# ADR-1314: Free trials for API consumers

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** An app consumer can have one free trial: usage in `[starts_at, ends_at)` is priced at zero.
  - **Starting.** `POST …/consumers` takes `trial_days` (1–365), and `PUT /v1/apps/{slug}/consumers/{id}/trial` takes `days` or `ends_at`. A new trial starts at the next UTC minute.
  - **Changing.** `PUT` on a running trial moves its end, never before the next minute. `DELETE` ends a running trial at the next minute, or removes one that has not started. A consumer whose trial has ended cannot get another (409).
  - **Pricing.** A trial minute is priced by the card in force at zero, with zero charged units. It is marked `trial` in statements and quotes. Trial units count toward no monthly allowance, tier position or volume total (ADR-950, ADR-951, ADR-1309), so the paid month starts counting after the trial.
  - **Where it applies.** Statements, quotes, spend caps (ADR-1306) and self-service (ADR-1304) share the trial through the consumer's quote options. Plan limits enforced at the gateway (ADR-953) still apply during a trial.
- **Why:** API businesses let new customers try the API before paying. Without trials, operators had to grant prepaid credit (ADR-1306) or create a free card and swap it out, which also changes the price for everyone else.
- **Consequences:**
  - **Never retroactive.** Trials only start or end at the next minute, so a minute a finalized statement billed never changes price, and no revision needs a credit.
  - **One trial per consumer.** Reusing a consumer for a second trial would need trial history to keep earlier statements exact; operators create a new consumer instead.
  - **Unpriced minutes stay unpriced.** A trial minute no card prices is still unpriced, so a statement with it cannot be finalized until a card exists, as before.
  - **App consumers only.** Customer-wide (platform tenant) statements price consumers by tenant cards and ignore app consumer trials. *ADR-1322 gives tenants their own trial.*
- **Rejected alternatives:**
  - Trial days on plans, applied when a consumer is assigned, would change plan storage and the plan update API for a setting operators can pass when creating the consumer.
  - Pricing trial minutes as unpriced would block finalizing every statement that covers a trial.
