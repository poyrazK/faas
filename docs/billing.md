# Billing

Gregale bills the account that owns an app. Plan prices and included capacity are generated from the platform limits table in [Plans and pricing](plans.md).

```bash
gregale usage --month 2026-09
gregale billing status
gregale billing portal
```

Usage is measured in GB-RAM-hours for running app instances. The plan also sets deployed-app, developer-app, concurrency, storage-layer, and idle-timeout limits. Apps parked by scale-to-zero do not accrue running-instance usage; storage and other explicitly metered services remain separate line items.

Free accounts have a zero monthly charge and are subject to the published limits. Paid plans are billed monthly through the account billing portal. Treat the portal as the source of truth for invoices, tax, payment methods, credits, and failed-payment recovery.

## FOCUS invoice export

Gregale provides a **partial FOCUS 1.4 Invoice Detail projection** for financial
reconciliation. It is not a fully conformant FOCUS dataset: required payment
terms are not yet stored. The export metadata and HTTP
`X-Gregale-FOCUS-Conformance: partial` header declare this limitation.

```bash
gregale billing export --month 2026-09 --out invoices.zip
gregale billing export --month 2026-09 --format csv --out invoices.csv
gregale billing export --month 2026-09 --format metadata --out metadata.json
```

The default ZIP contains `gregale-invoice-detail-2026-09.csv` and `metadata.json`
from the same invoice snapshot. Metadata contains the FOCUS data generator,
dataset instance and exact column schema, plus `x_GregaleProjection` with the
CSV SHA-256, row count, totals by currency, excluded invoice counts, and known
gaps. Use ZIP when CSV and metadata must match: independent downloads can see
newer billing webhooks. Files are created with owner-only permissions; existing
files are preserved. CSV and metadata can go to stdout; ZIP requires an explicit
`--out PATH` or `--out -`.

`GET /v1/billing/focus?month=2026-09&format=zip` exposes the same export. It
requires `usage:read`, uses the invoice-history session MFA gate, and remains
available to suspended accounts for billing recovery. The optional `format`
is `zip`, `csv`, or `metadata`. Unknown or repeated query parameters are rejected.
Downloads are private and not cached.

The required month selects invoices by **period end in the UTC month**, matching
`GET /v1/invoices`; it does not select by issue date or prorate usage. An invoice
ending at `2026-10-01T00:00:00Z` belongs to the October export. At most 1,000
stored invoices are accepted per month, including drafts and voids, and each
artifact is at most 3 MiB. Exceeding the invoice limit returns a problem response
with the limit and observed count, without producing a truncated export.

### Mapping

The CSV contains the 18 mandatory Invoice Detail columns in alphabetical order.
Conditional payment-currency and purchase-order columns are omitted because
their source data is unavailable. Empty CSV fields represent nulls. Dates use
UTC RFC 3339 with a `Z` suffix; amounts are exact decimal strings, never floats.

| FOCUS field | Gregale mapping |
|---|---|
| `BilledCost`, `ChargeCategory` | One `Usage` aggregate of `total_cents - tax_cents`; a separate `Tax` row when tax is nonzero. Their sum equals the stored invoice total. Subtotal, paid amounts, refunds, and credits are not subtracted again. |
| `BillingAccountId`, `BillingCurrency` | Authenticated Gregale account ID and uppercase ISO 4217 billing currency. Only two-decimal currencies are supported by this cents-based projection. |
| `BillingPeriodStart`, `BillingPeriodEnd` | Stored invoice period boundaries. |
| `InvoiceId`, `ReferenceInvoiceId` | Provider invoice/order document ID. Original invoices reference themselves; payment/charge handles are not used. |
| `InvoiceDetailId`, `InvoiceDetailGrain` | Stable local invoice ID plus `:charges` or `:tax`; JSON grain contains `x_GregaleAggregation` identifying that component. These are aggregates, not provider line-item IDs. |
| `InvoiceDetailDescription` | Description of the non-tax or tax aggregate. |
| `InvoiceIssuerName` | `Polar` or `Paddle` for those merchants of record; `Gregale` for Stripe processing. Issuer brand normalization does not recover an invoice's legal entity name. |
| `InvoiceIssueStatus` | Stored `open`, `paid`, and `uncollectible` invoices are `Issued`. Payment state does not imply a draft invoice. Draft and void invoices are excluded and counted in metadata. |
| `InvoiceDetailCreated`, `InvoiceDetailLastUpdated` | Creation and update timestamps of the local invoice projection. These are not provider issue dates. |
| `InvoiceIssueDate`, `PaymentDueDate`, `PaymentTerms` | Empty because those provider fields are not stored. `PaymentTerms` is a required non-null field in FOCUS: this is an explicit conformance gap. |

Zero-cost issued invoices retain one non-tax row; zero tax rows are omitted.
`Usage` is the projection's non-tax aggregate classification. Provider line-item
classifications are not stored, so the export cannot distinguish a pure plan
purchase from a mix of usage, purchases, and credits. This semantic gap is also
declared in metadata; strict charge categorization needs provider line items.
Malformed currencies, unsupported currency precision (for example JPY or KWD),
inconsistent amounts, missing identifiers, and unrepresentable dates fail the
entire download with HTTP 409. No amounts or timestamps are guessed.

### Remaining gaps

Only locally persisted invoices are projected; the export does not fetch or
reconcile the provider's complete invoice history. Refunds and credit notes
need their own financial documents and original-invoice links before they can
be exported as separate adjustments. Conditional payment-currency conversion
and purchase-order data also need provider ingestion.

The next step is to persist invoice payment terms, issue/due dates, legal issuer
identity, and provider line items, including correction lineage. A historical
cost ledger with service/resource identifiers, quantities, units, and price
snapshots is then needed for the **Cost and Usage** dataset. Current plan prices
cannot reliably reconstruct past list, contracted, or effective costs.

The mapping is based on the official
[FOCUS 1.4 Invoice Detail specification](https://focus.finops.org/docs/specification/v1-4/datasets/invoice-detail/).
`make focus-contract-check` checks the pinned upstream columns, exact
reconciliation, snapshot metadata, ownership, export bounds, and CLI downloads.
