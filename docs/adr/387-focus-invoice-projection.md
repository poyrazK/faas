# ADR-387 · FOCUS invoice projection

- **Status:** accepted
- **Date:** 2026-09-30
- **Decision:** Expose a read-only, account-scoped partial FOCUS 1.4 Invoice
  Detail projection from stored provider invoices, with explicit source gaps.
- **Why:** Invoice totals and taxes are available, while historical quantities,
  prices, payment terms, and provider line items are not. FOCUS 1.4 Invoice
  Detail supports financial reconciliation without fabricating those prices.

`GET /v1/billing/focus` and `gregale billing export` require an explicit month
and return CSV, metadata, or a default ZIP containing both. The month uses the
existing invoice-history period-end filter. The API takes one bounded store
snapshot, validates everything, then writes an artifact. Exports above 1,000
stored invoices fail without pagination or truncation. Artifacts are bounded
to 3 MiB and identifiers to 256 bytes in the central limits table.

The same usage-read scope and session MFA gate as invoice history apply.
Suspended accounts retain access for billing recovery. Account ownership is
checked again during projection. Successful downloads are private/no-store;
client files are owner-readable and cannot overwrite existing paths.

Issued invoices contribute one non-tax aggregate of total minus tax and an
optional nonzero tax row. Totals use integer arithmetic, including arbitrarily
large multi-invoice reconciliation totals. Refunds, credits, and paid balances
do not modify the original invoice total a second time. Provider document IDs
identify original invoices; detail IDs identify local aggregate components.
Draft and void invoices are excluded with metadata counts. Unknown statuses,
unsupported minor-unit precision, malformed source data, and invalid dates
fail the entire artifact. `Usage` is the projection's default non-tax aggregate
classification, not a metered usage assertion. Provider classifications are
unavailable, so a pure plan purchase cannot be distinguished from mixed
usage/purchase/credit charges. Metadata declares that semantic conformance gap.

The export publishes all 18 mandatory columns. PaymentTerms is empty and
explicitly declared as a non-null conformance gap. Nullable issue/due dates are
empty rather than replaced by local timestamps. Conditional FX and purchase
order columns are omitted. Merchant-of-record brands identify Polar and Paddle;
Stripe processing uses Gregale as issuer. Exact legal identities remain a
provider-ingestion follow-up. Metadata includes the FOCUS schema and dataset
identity plus custom gap descriptions, per-currency totals, and CSV SHA-256.
Independent CSV/metadata calls can observe different webhook versions; ZIP
provides both from one snapshot.

- **Consequences:** The standards registry remains `partial`. Tests derive
  names/types/non-null constraints from unmodified FOCUS 1.4 reference documents
  pinned to their release commit and CC BY 4.0 license. Tests permit the declared
  PaymentTerms gap and do not claim certification. `make focus-contract-check`
  runs reconciliation, ownership, query validation, bound, metadata, SDK, and
  CLI tests in CI. No change to metering, plan economics, charging, provider
  ownership, invoice persistence, or VM lifecycle is made.
  Generated Node and Python downloads preserve raw artifact bytes while
  retaining structured problem errors. Their pinned generator post-processors
  also correct the existing binary debug-request export through the same rule.
  SDK regeneration includes the previous OTLP contract corrections.
- **Rejected alternatives:** Reconstructing historical resource costs from
  today's plan prices; inventing payment terms or issue dates; presenting
  cash refunds as new invoice documents; silently truncating a billing export;
  calling the projection fully FOCUS-conformant. Provider terms, line items,
  correction documents, and a historical pricing ledger are subsequent work.

Rollback removes the new read-only route and CLI command. No migration or
provider-side billing mutation is involved.
