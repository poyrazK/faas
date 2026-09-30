# ADR-383 · Authenticated provider invoice refresh

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Add an account-owned refresh operation for persisted invoices,
  fetching provider facts and updating details plus lifecycle atomically.
- **Why:** Webhook snapshots can omit prices, paginated lines, and invoice facts;
  existing invoices need enrichment before complete history discovery is useful.

`POST /v1/invoices/{id}/refresh` and `gregale billing refresh-invoice ID` use the
invoice-history scope and session MFA gate. The local invoice must belong to the
caller and to the configured provider. The provider-qualified billing identity
and returned document/customer IDs must agree. No caller-supplied provider ID,
URL, monetary value, or invoice fact is accepted.

An optional billing provider reader fetches the existing Stripe invoice, Paddle
transaction, or Polar order. Stripe's legacy apid boot path attaches a separate
read-only reader using the env-overlaid Stripe configuration, while keeping its
webhook/payment dispatch intact. Refresh resolves the qualified Stripe identity
and never falls back to another provider's legacy account handles. Stripe retrieves all line pages and resolves opaque
price/plan IDs with a per-operation cache. Requests, responses, and duration are
bounded in `pkg/api/limits.go`; pagination cannot claim completeness on failure.
Provider omissions and unknown classifications remain visible in FOCUS metadata.
Buyer identity and order creation dates never substitute for invoice issuer or
issue dates. Provider HTTP calls remain outside export and database transactions.

Refresh changes only invoice details and their export-record history. It checks
remote currency, total, and tax against the captured local invoice, then uses
the captured update timestamp as an optimistic concurrency check under a row
lock. Concurrent webhooks/refunds force a retry instead of overwriting newer
facts or payment state. MemStore implements the same check under its mutex.
Generated sqlc statements persist details and lifecycle in one transaction.
Exact replay preserves detail timestamps. apid remains the only invoice writer.

This operation enriches known historical invoices; it does not discover missing
documents, claim provider history completeness, move financial amounts, send
dunning messages, change entitlements, or invent legal identity/terms. Full
history enumeration and correction-document ingestion remain subsequent work.

Mock provider HTTP tests verify authorization, pagination, price hydration,
exact money, cancellation, limits and failures. Both store suites verify stale
refresh rejection, lifecycle updates, rollback and financial-field preservation;
API/CLI tests exercise ownership, scopes, metadata improvement and replay.
