# ADR-381 · Provider invoice facts for FOCUS

- **Status:** accepted
- **Date:** 2026-09-30
- **Decision:** Persist bounded, provider-neutral invoice facts in an atomic
  JSONB snapshot and use reconciled, classified line items in FOCUS exports.
- **Why:** ADR-380's aggregate projection discarded provider line items and
  settlement details, preventing accurate purchase/consumption classification.

`state.Invoice.Details` and `billing.InvoiceData.Details` carry issuer name,
invoice issue/due dates, payment terms, and an optional line snapshot. A missing
field means unavailable. Signed provider webhook adapters normalize facts;
apid remains the invoice writer. A timestamp migration adds `invoices.details`
with a JSON-object CHECK. Invoice reads/upserts use generated sqlc queries.

The natural-key upsert merges supplied scalar facts. An omitted line snapshot
preserves existing lines; a supplied snapshot replaces the entire list, even
when incomplete. Exact replay preserves each line's local first-ingestion and
last-change timestamps. New/changed line IDs receive appropriate timestamps.
A bounded first-seen map retains creation dates after line removal/reappearance.
Both stores deep-copy caller-owned data. An atomic database statement prevents
read/merge/write races, while retaining historical plan/refund/credit behavior.

Provider differences remain explicit:

- Stripe stores the invoice's public business name, effective/finalized issue
  date, due date, and settlement terms expressed as `Due by <actual due date>`.
  It supports legacy/new tax shapes and separates inclusive tax and discounts.
  Expanded recurring prices distinguish licensed purchases from metered usage.
  Opaque price IDs remain unclassified; missing/true `has_more` is incomplete.
- Paddle uses calculated line totals less tax, supplied `billed_at` and actual
  structured payment terms. Gregale's provisioned monthly/overage price
  descriptions distinguish purchases from consumption, including flat-rate
  overage prices. Duplicate price IDs cannot form stable detail identities and
  make the snapshot incomplete. Adjusted invoice totals may not match original
  line totals; those invoices retain aggregate rows.
- Polar retains order items and classifies legacy expanded price types (`fixed`
  and `metered_unit`). Current payloads without those price facts remain
  unclassified. Buyer billing names and order creation dates never stand in for
  issuer identity or invoice issue dates. Unallocated order discounts can leave
  a reconciliation gap; no proportional allocation is invented.

Detailed rows require a complete, nonempty, classified snapshot whose integer
net and tax sums match the stored invoice total/tax exactly. Arbitrary precision
sums reject overflow. Stable namespaced UUIDs identify each invoice/line/component;
grain records provider line IDs and charge/tax components. Negative amounts use
exact decimal formatting. Sparse or inconsistent snapshots use the prior
aggregate projection with counted reasons, preserving financial totals.

Metadata reports detailed/aggregate coverage and missing terms, issuer names,
and dates. MissingRequiredFields lists PaymentTerms only when delivered rows
lack it. Conformance remains `partial`: provider history backfill, exact legal
identity coverage, original-invoice correction links, separate refund/credit
documents, conditional FX/PO fields, and Cost and Usage allocation still need
work. No provider API calls occur during export.

Detail timestamps currently track local provider-line facts. Shared invoice
field changes and the later creation of a separate tax component do not have
independent detail timestamps; metadata declares this lifecycle coverage gap.

Limits live in `pkg/api/limits.go`: 1,000 invoices, 1,000 items per invoice,
10,000 exported rows, 10,000 retained line IDs per invoice, 256-byte identifiers,
4,096-byte descriptive fields, and
3 MiB artifacts. Row/byte limits return 422 before download; invalid facts return
409. Synthetic provider fixtures and real Postgres tests cover normalization,
reconciliation, sparse updates, replay, detail timestamps and tenant isolation.
