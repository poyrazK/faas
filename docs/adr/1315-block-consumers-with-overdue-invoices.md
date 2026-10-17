# ADR-1315: Block consumers with overdue invoices

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** An app can refuse API consumers whose exported invoice is still unpaid some days after its due date.
  - **Policy.** `PUT /v1/apps/{slug}/consumer-billing-policy` sets `block_overdue_after_days` (0–365, where 0 blocks as soon as the invoice is past due). `GET` reads it, and `DELETE` removes it.
  - **What counts.** An app consumer statement exported to Stripe, Paddle or Lago whose payment status (ADR-1302) is `open` or `uncollectible`, with an amount remaining and a due date at least that many days ago. Drafts, which have no due date, never count. Neither do paid, voided or deleted invoices.
  - **Holds.** Every 5 minutes an apid worker recomputes one hold per consumer, naming its oldest qualifying invoice, and lifts every other hold. The recomputation runs as a single statement under an advisory lock, so replicas never report a change twice. Placing and lifting are audited (`api_consumer.overdue_blocked`, `api_consumer.overdue_unblocked`). `GET /v1/apps/{slug}/consumer-overdue-holds` lists them.
  - **Enforcement.** The gateway reads the hold with the consumer's plan policy, on any plan, like a spend cap (ADR-1306). It answers 402 `consumer_invoice_overdue` with the invoice's due date. The refused request is not billed.
- **Why:** With collection (ADR-1311) invoices reach customers on their own, so operators wanted unpaid customers cut off without watching the overdue digest and revoking keys by hand.
- **Consequences:**
  - **Lag.** A hold follows payment status, which ADR-1302 reads every 6 hours for an overdue invoice. While an invoice holds its consumer, each recomputation brings its next read within 15 minutes. With holds recomputed every 5 minutes and the gateway caching the policy for 15 seconds, paying lifts a hold within about 20 minutes. Operators can delete the policy to lift every hold at the next check.
  - **App consumers only.** Customer-wide (platform tenant) statements are not checked. Tenants keep their request budgets (ADR-240). *ADR-1324 adds an account rule for tenants.*
  - **Statement totals unchanged.** A blocked consumer adds no usage, so its later statements only bill what it used before the hold.
- **Rejected alternatives:**
  - Revoking consumer keys would lose them for good and need reissuing after payment.
  - Blocking from the provider's webhooks would be faster but needs a signing secret per provider and account, which ADR-1302 declined.
