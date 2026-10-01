# ADR-385 · Provider invoice history discovery and backfill

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Add an authenticated, customer-scoped, one-page provider-history
  import for Stripe, Paddle, and Polar. Each provider adapter controls its
  resource path and pagination; caller input supplies only a bounded opaque
  cursor and page size. Verify the returned customer on every document.
- **Why:** Webhooks and payment-time persistence can miss earlier provider
  invoices. FOCUS exports must be able to discover those documents without
  accepting provider URLs, another tenant's customer IDs, or unbounded reads.

`POST /v1/invoices/backfill` requires `usage:read` and session MFA, like invoice
history and refresh. It scans at most 25 provider records, imports valid EUR
invoices with explicit billing periods, and returns scanned/imported/skipped
counts with a provider/customer-bound cursor. A cursor resumes a page rather
than claiming a complete point-in-time snapshot; repeat backfills can discover
later provider changes. Invalid or unrepresentable provider records are skipped
while the provider cursor advances.

The state operation is insert-only on `(account_id, provider,
provider_invoice_id)`. Existing webhook records remain authoritative and a
collision is a skip. One provider page commits atomically in PostgreSQL; the
MemStore implementation holds its lock for the page. Imported rows carry an
explicit `unknown` historical plan because a current account plan cannot prove
the entitlement active during an old billing period. Credit proration rejects
this sentinel instead of consulting the current plan. The migration's down
operation refuses rollback until unknown-plan imports are removed or assigned
a verified historical plan.

Provider facts that fail customer, currency, amount, status, or period checks
are not imported. Provider line enrichment remains the separate refresh
operation, bounded to its existing limits. This supplies history discovery but
does not complete FOCUS conformance: provider documents can omit dates or
periods, pagination is not snapshot isolation, and plan history, corrections,
and credit notes remain separate gaps.
