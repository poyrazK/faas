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
reconciliation. Signed billing webhooks retain provider line items and invoice
terms/dates where available; an authenticated refresh can enrich existing
invoices. The export remains partial; its metadata and
`X-Gregale-FOCUS-Conformance: partial` header describe source coverage and gaps.

```bash
gregale billing export --month 2026-09 --out invoices.zip
gregale billing export --month 2026-09 --format csv --out invoices.csv
gregale billing export --month 2026-09 --format metadata --out metadata.json
```

The default ZIP contains `gregale-invoice-detail-2026-09.csv` and `metadata.json`
from the same invoice snapshot. Metadata contains the FOCUS data generator,
dataset instance and exact column schema, plus `x_GregaleProjection` with the
CSV SHA-256, row count, totals by currency, excluded invoice counts, and known
gaps. `SourceCoverage` counts detailed invoices, missing invoice facts, and
aggregate fallback reasons (`unavailable`, `incomplete`, `empty`, `unclassified`,
`totals_mismatch`, or `tax_in_non_tax_lines`). `UntrackedLifecycleRecords` counts
rows with no persisted export history; `LegacyLifecycleRecords` counts tracked
rows whose original creation time is uncertain. Use ZIP when CSV and metadata must
match: independent downloads can see newer billing webhooks. Files are created
with owner-only permissions; existing
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
artifact is at most 3 MiB. Each invoice can retain at most 1,000 items; exports
contain at most 10,000 rows. Descriptive fields are bounded to 4,096 bytes and
identifiers to 256 bytes. Each invoice retains at most 10,000 historical line IDs
to preserve creation dates after removal. Export lifecycle history retains at
most 20,002 records per invoice (two components per historical line plus two
aggregate rows); a delivery exceeding this bound fails atomically. Exceeding
invoice, row, or artifact bounds returns 422
with the limit and observed count, without producing a truncated export.

### Mapping

The CSV contains the 18 mandatory Invoice Detail columns in alphabetical order.
Conditional payment-currency and purchase-order columns are omitted because
their source data is unavailable. Empty CSV fields represent nulls. Dates use
UTC RFC 3339 with a `Z` suffix; amounts are exact decimal strings, never floats.

| FOCUS field | Gregale mapping |
|---|---|
| `BilledCost`, `ChargeCategory` | Complete, classified line snapshots produce separate charge and nonzero tax rows. Net line costs and taxes must exactly reconcile to stored invoice totals. Otherwise one `Usage` aggregate of `total_cents - tax_cents` and a separate nonzero `Tax` row are used. Subtotal, paid amounts, refunds, and credits are not subtracted again. |
| `BillingAccountId`, `BillingCurrency` | Authenticated Gregale account ID and uppercase ISO 4217 billing currency. Only two-decimal currencies are supported by this cents-based projection. |
| `BillingPeriodStart`, `BillingPeriodEnd` | Stored invoice period boundaries. |
| `InvoiceId`, `ReferenceInvoiceId` | Provider invoice/order document ID. Original invoices reference themselves; payment/charge handles are not used. |
| `InvoiceDetailId`, `InvoiceDetailGrain` | Detailed rows use stable UUIDs from the local invoice ID, provider line ID, and charge/tax component; grain contains `x_GregaleProviderLineId` and `x_GregaleComponent`. Fallback IDs/grain retain the aggregate mapping (`:charges`/`:tax`, `x_GregaleAggregation`). |
| `InvoiceDetailDescription` | Provider item description, or the aggregate description on fallback. |
| `InvoiceIssuerName` | Provider invoice business name when supplied (Stripe `account_name`); otherwise `Polar`, `Paddle`, or `Gregale` merchant brands. Exact legal identity is not guaranteed. |
| `InvoiceIssueStatus` | Stored `open`, `paid`, and `uncollectible` invoices are `Issued`. Payment state does not imply a draft invoice. Draft and void invoices are excluded and counted in metadata. |
| `InvoiceDetailCreated`, `InvoiceDetailLastUpdated` | Each exported charge/tax or aggregate record has its own persisted local creation and last-update timestamps. Changes to any exported column advance the affected record; replay, sparse deliveries, and identical reappearance preserve dates. These never substitute for issue dates. |
| `InvoiceIssueDate`, `PaymentDueDate`, `PaymentTerms` | Supplied invoice facts, with nullable dates empty when unavailable. `PaymentTerms` is required and is listed in metadata `MissingRequiredFields` when any delivered invoice lacks it. |

Zero-cost issued invoices retain a non-tax row; zero tax rows are omitted.
Negative lines retain their sign and category. Detail lists must be complete,
nonempty, classified, and reconcile exactly; no provider pagination is fetched
at export time. Fallback non-tax aggregates retain `Usage` and a declared
classification gap. A sparse webhook preserves prior scalar facts/omitted
lines; a supplied line list replaces the snapshot, including its completeness.

| Provider | Captured facts and classification |
|---|---|
| Stripe | Public invoice business name, effective/finalized issue date, due date, and terms expressed as `Due by <actual due date>`. Expanded recurring prices identify licensed purchases or metered usage; refresh resolves opaque price/plan IDs and retrieves every line page. Both tax shapes, inclusive tax, and discounts are supported. `has_more` must explicitly be false. |
| Paddle | Actual structured payment terms and `billed_at`; calculated line total minus tax gives net cost after discounts. Gregale's provisioned monthly/overage descriptions identify Purchase/Usage. Exact issuer and due date are not supplied by this transaction payload. Duplicate price IDs mark the list incomplete; original lines may not reconcile to adjusted totals. |
| Polar | Order items and their amounts/taxes. Legacy expanded price types distinguish fixed purchases from metered usage; current payloads without price facts remain unclassified. Invoice terms, actual issue/due dates, and issuer identity remain unavailable. Buyer billing names and order creation dates are never substituted. Unallocated order discounts can require aggregate fallback. |

Historical invoices can be enriched by provider deliveries or the refresh
operation below. The export remains partial even when every invoice in a
particular month has payment terms.

Malformed currencies, unsupported currency precision (for example JPY or KWD),
inconsistent amounts, missing identifiers, and unrepresentable dates fail the
entire download with HTTP 409. No amounts or timestamps are guessed.

### Refresh invoice facts

Use an ID from `gregale invoices` to refresh a locally stored invoice:

```bash
gregale billing refresh-invoice INVOICE_ID
```

`POST /v1/invoices/{id}/refresh` exposes the same operation with no request body
or query parameters. It requires `usage:read` and the invoice-history session MFA
gate, and is available during billing suspension. The invoice must belong to the
caller and the deployment's configured billing provider. The response reports
`invoice_id`, `provider`, `line_items`, `detailed`, `source_gap` when present, and
`updated_at`. `detailed` describes line reconciliation/classification; it does
not certify that all required invoice facts or historical documents are present.
Export metadata reports those remaining gaps.

Refresh performs authenticated reads of the existing Stripe invoice, Paddle
transaction, or Polar order. Stripe fetches its dedicated line endpoint in pages
of up to 100 and resolves opaque price/plan IDs once per operation. Every refresh
is limited to 32 provider reads, 1,000 lines, 4 MiB per response, 20 seconds per
read, and two minutes overall. Exceeding read, line, or response bounds returns
422 with the limit, observed count, and documentation link; no partial snapshot
is stored. Provider failures return 503. Unsupported providers return 501.

Remote customer/document IDs, currency, total, and tax must match the captured
local invoice. Refresh updates facts and record lifecycle atomically and leaves
amounts, payment state, refunds, credits, plan, and entitlements unchanged. A
concurrent webhook or refund produces 409; retrieve the current invoice and retry
once the provider deliveries have reconciled. Exact replay preserves record
lifecycle dates. Unknown classifications and unavailable issuer/terms/dates
remain source gaps; refresh never substitutes buyer identity or order creation
for invoice issuer or issue date.

Refresh enriches known invoices only. It does not enumerate provider history or
import documents absent from Gregale. Provider reads happen only during refresh,
so exports remain a projection of stored facts.

### Remaining gaps

Only locally persisted invoices are projected; the export does not fetch or
reconcile the provider's complete invoice history. Refunds and credit notes
need their own financial documents and original-invoice links before they can
be exported as separate adjustments. Conditional payment-currency conversion
and purchase-order data also need provider ingestion.

Lifecycle tracking starts with local ingestion. Existing invoices cannot recover
unknown historical record creation times. Their available local observations
are retained and flagged; exports count both untracked and legacy records and
declare this historical gap when present. Facts and lifecycle history are
updated in one transaction so downloads cannot see mismatched snapshots.

The next invoice steps are provider history discovery/backfill, remaining
price classifications and legal issuer coverage, and correction-document
lineage. A historical cost ledger with
service/resource identifiers, quantities, units, and price snapshots is then
needed for the **Cost and Usage** dataset. Current plan prices
cannot reliably reconstruct past list, contracted, or effective costs.

The mapping is based on the official
[FOCUS 1.4 Invoice Detail specification](https://focus.finops.org/docs/specification/v1-4/datasets/invoice-detail/).
`make focus-contract-check` checks the pinned upstream columns, exact
reconciliation, snapshot metadata, ownership, export/refresh bounds, provider
reads, lifecycle concurrency, and CLI operations.
