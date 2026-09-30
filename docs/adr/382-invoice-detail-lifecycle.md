# ADR-382 · Invoice detail lifecycle tracking

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Persist bounded lifecycle history for each exported invoice
  charge/tax record, using fingerprints of its actual non-timestamp values.
- **Why:** ADR-381's provider-line timestamps cannot describe shared invoice
  changes or the separate creation of tax records required by FOCUS 1.4.

A pure `pkg/focus/invoicedetail` renderer owns the Invoice Detail row values.
The state layer adapts stored invoices to this renderer; exports use the same
rows. Fingerprints cover every exported value except the two lifecycle
timestamps. Stable detail IDs remain unchanged. Charge and tax rows, including
aggregate fallback rows, have independent creation and update timestamps.

An additive timestamp migration adds `invoices.detail_lifecycle` as a bounded
JSON object. The snapshot records tracking start, historical coverage, and
per-detail fingerprints/dates. Removed rows retain their history; identical
reappearance/replay preserves timestamps, while changed values advance only
the affected rows. Invoice facts omitted by a sparse webhook remain preserved.

apid remains the invoice writer. PostgreSQL performs the existing natural-key
upsert and lifecycle update in one transaction, retaining the row lock through
both generated sqlc statements. MemStore does the same work under its mutex.
Incoming provider data cannot supply lifecycle history. Financial, refund,
credit and historical-plan semantics remain unchanged.

Existing invoices cannot recover unknown historical detail creation times.
Their first tracked snapshot retains available local creation observations and
marks historical uncertainty. Exports also count records without lifecycle
history, and reject stale fingerprints or malformed lifecycle data. New rows
observed since tracking began receive their own creation instant. Coverage
metadata distinguishes these cases; full FOCUS conformance remains partial.

The record-history bound is twice the retained provider-line-ID limit plus two
aggregate records, defined in `pkg/api/limits.go`. Shared row rendering and
fingerprint tests, both store suites, real PostgreSQL concurrency/rollback
checks, and FOCUS export tests verify lifecycle behavior and unchanged totals.
