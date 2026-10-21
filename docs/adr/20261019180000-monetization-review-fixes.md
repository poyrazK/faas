# ADR-20261019180000: One export attempt at a time, retargetable refusals, floored minimums and fresh overdue holds

- **Status:** accepted
- **Date:** 2026-10-19
- **Decision:** An end-to-end review of API monetization found four ways it could bill wrongly or block a paying customer. Each gets a fix.
  - **One export attempt at a time.** An export attempt claims its statement's export row for `StatementInvoiceExportClaimTTL` (15 minutes) and releases it when it ends. A concurrent attempt, such as a retried request or an overlapping billing run, gets 409 `statement_invoice_export_in_progress` instead of creating a second Paddle transaction or Lago invoice; Stripe was already protected by idempotency keys. The claim reads the row as the previous attempt left it, so a waiting attempt that follows a finished one returns its invoice. A crashed attempt's claim expires. Billing runs treat the 409 as a retry, and renew their run lease before each customer, so a long batch keeps its run instead of a second worker billing the same customers.
  - **Retargetable refusals.** When the provider definitively refuses to create the invoice, the export records `rejected_at`, with nothing created there. Refusals are a rejected key, or a 4xx other than a timeout, conflict or rate limit, such as an unknown customer. The next export may then name another customer or provider: the row becomes its next `attempt`. Before, the row kept the first target and every retry was a 409, so a typo blocked the statement for good. Stripe idempotency keys carry the attempt after the first one, so a retry is not served the cached refusal. An export whose invoice exists, or whose creation timed out, cannot be retargeted.
  - **Finalized minimums fall only by new usage.** Prorated fee segments (ADR-20261019100332) could shorten a card's share after its minimum was finalized, through a later card or a revocation, which produced a negative top-up. Statement totals cannot be negative, so the month could no longer be revised. A minimum top-up now falls only by usage billed since the finalized revisions, for app consumers and platform tenants alike. A revocation ends prorating cards' shares unless a finalized revision priced the month before it: the check reads `as_of`, not `finalized_at`.
  - **Fresh overdue holds.** A consumer or tenant is held only by an invoice whose payment status Gregale read within `ConsumerOverdueStatusMaxAge` (24 hours); held invoices are read every 15 minutes. When the account's key is rotated or its connection removed, the hold lifts instead of outlasting a payment Gregale cannot see. The account gets a daily email naming the provider to reconnect.
- **Why:** The review (2026-10-19) found these on main. Each either charges a customer twice, leaves a statement unbillable, or blocks a customer who paid.
- **Consequences:**
  - **Retry after 409.** A caller that sees `statement_invoice_export_in_progress` retries; a run retries on its own.
  - **Overdue blocking pauses** while invoice sync is broken. Reconnecting the key resumes it within a sync.
  - **Late usage** still lowers a finalized minimum top-up, as ADR-1316 says; only a shrinking share no longer does.
  - **Migration.** `usage_statement_invoice_exports` gains `attempt`, `rejected_at`, `claim_token` and `claimed_until` (expand); their checks are added `NOT VALID` and validated by a second migration.
- **Rejected alternatives:**
  - An advisory lock held across the provider calls would pin a database connection for up to minutes per export.
  - Deleting a failed export row would lose the evidence that a provider may hold an invoice for it.
  - Refunding a shrunk minimum with a negative revision would need negative statements, which ADR-1325 rejected.
