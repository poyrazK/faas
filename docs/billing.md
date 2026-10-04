# Billing

Gregale bills the account that owns an app. Plan prices and included capacity are generated from the platform limits table in [Plans and pricing](plans.md).

```bash
gregale usage --month 2026-09
gregale billing status
gregale billing portal
```

Usage is measured in GB-RAM-hours for running app instances. The plan also sets deployed-app, developer-app, concurrency, storage-layer, and idle-timeout limits. Apps parked by scale-to-zero do not accrue running-instance usage; storage and other explicitly metered services remain separate line items.

Free accounts have a zero monthly charge and are subject to the published limits. Paid plans are billed monthly through the account billing portal. Treat the portal as the source of truth for invoices, tax, payment methods, credits, and failed-payment recovery.

## Retained usage costs and forecasts

This financial visibility preview reports supported usage costs and budget
drafts. Budget enforcement is unavailable: drafts do not stop workloads or
send spending notifications. Coverage and unavailable bill components are
reported explicitly.

```bash
gregale billing costs --month 2026-10 --json
gregale billing forecast --json
```

`GET /v1/billing/costs` and `GET /v1/billing/forecast` provide read-only,
account-scoped access with `usage:read` and the invoice-history session MFA gate.
The optional `month=YYYY-MM` selects a UTC calendar usage month and defaults to
the current month. Unknown and repeated parameters are rejected.

Compute and interface egress evidence retain application/job, project,
environment, and deployment identity at first observation. Names remain those
of the observed workload after rename or deletion. Usage replay preserves source
identity; compute and cumulative network observations cannot duplicate charges.
Historical prices are recorded before sampling and linked by version. Evidence
from before price capture is marked unpriced rather than priced with today's rate.

Retained corrections append a negative quantity linked to the original source.
They preserve its price, period and workload identity, record an actor and reason,
and cannot subtract more than the original usage, including concurrent corrections.
Replay cannot add a second correction. Original evidence and price terms cannot
be rewritten; account deletion can remove its financial history. Provider delivery
continues to use its existing source until correction reconciliation is accepted.

All amounts use integer millicents (1,000 millicents per cent). One shared
account allowance applies to each usage meter for the month. When plans change,
the largest recorded grant remains available for that month; its quantities are
shared proportionally across price versions. Within each version, net cost and
allowance value are allocated by workload quantity using stable largest-remainder
rounding. Allocated amounts sum exactly to the meter totals. Shadow and disabled
egress contracts are reported as nonbillable quantities. The existing provider
delivery remains unchanged while ledger reconciliation is validated.

Coverage reports the retention start, complete sampling minutes, freshness, and
unpriced quantities. A missing sample does not establish zero usage. Quantity
run-rate forecasts need one full day of complete, fresh evidence and unchanged
price terms; unavailable projections omit amounts and explain why. Interface
egress observations currently have delayed source coverage, so they do not claim
complete transfer forecasts. Forecasts do not control admission.

The current cost surface covers retained compute and interface egress. It reports
subscription/add-ons, credits/adjustments, tax, and external/managed resource
meters as missing bill components. `known_usage_millicents` is the priced usage
subtotal, not an invoice total. Provider invoices retain their own periods and
are returned separately; `invoice_reconciliation` is `not_reconciled` until the
full reconciliation gate passes. The total-bill estimate remains unavailable.
Reports exceeding 10,000 allocation groups fail with 422 instead of returning
truncated totals. Financial history is retained independently of minute-level
usage cleanup and is erased with the owning account's final deletion.

## Budget previews

```bash
gregale billing budget-preview --file budget.json --json
```

`POST /v1/billing/budgets/preview` accepts a proposed budget spec and performs
no writes. It uses the current UTC usage month, validates account-owned scope,
and reports known attributed spending, coverage, affected workloads, and
workloads that can continue spending. The API uses the same `usage:read` and
session MFA gates as cost reports. The CLI file contains the spec directly;
the REST request wraps it in `{"spec": ...}`.

For example, this spec would select previews while production continues:

```json
{
  "name": "Preview spending",
  "scope": {"kind": "account"},
  "currency": "EUR",
  "meters": ["compute"],
  "basis": "net_usage",
  "limit_millicents": 1000000,
  "notify_millicents": [800000],
  "mode": "monitored",
  "action": "stop_previews",
  "drain_seconds": 30,
  "resume_rule": "manual",
  "enabled": true
}
```

The limit above is EUR 10.00. Available scopes are account, project,
environment, application, and job; resource scopes use their stable UUID.
Preview identity comes from Gregale's persisted preview metadata. Environment
membership currently comes from live deployment scopes. Historical costs retain
older identities even when the current target list changes.

Responses explain the difference between notifying, rejecting new traffic,
stopping previews, suspending background work, and suspending every workload in
a scope. Rejecting traffic leaves running compute able to spend. Broad selective
responses leave other workloads able to spend. Net usage applies the shared
account allowance once; gross usage measures cost before that allowance.
Strict resource scopes require gross compute, and a broad strict policy must
stop every covered workload.

Enforcement currently reports `enforcement_ready: false`. Policy activation,
durable decisions, independent holds, stopping acknowledgements, and strict
compute reservations are still under implementation. A successful preview does
not activate a spending limit. Monitored responses can exceed a threshold while
usage arrives or workloads drain. Full-invoice hard limits are not implied by
compute controls.

## Budget policy drafts and audit

The console's Usage page can preview responses and save disabled budget drafts.
It uses live account-owned project, environment, app and job lists. The editor
parses EUR text to exact millicents and shows affected and continuing workloads;
a partial cost observation remains labelled partial. Saved drafts do not stop
usage or send threshold notifications.

```bash
# budget-draft.json contains the spec above with enabled set to false.
gregale billing budgets create --file budget-draft.json --key previews-october --json
gregale billing budgets list --json
gregale billing budgets get --json POLICY_ID
gregale billing budgets update --file budget-draft.json --expected-revision 1 --key previews-update --json POLICY_ID
gregale billing budgets history --after-revision 0 --limit 100 --json POLICY_ID
gregale billing budgets delete --expected-revision 2 --key previews-delete --json POLICY_ID
```

Read operations require `usage:read`; mutations require `admin`. Session MFA
applies to both. REST mutations require an `Idempotency-Key` (1..255 bytes).
The Go client supplies one automatically, and CLI `--key` supplies a stable
key for retries across processes. Reusing a creation key retains one identity
even after HTTP replay-cache retention; it cannot overwrite a changed or deleted
policy. HTTP policy bodies are bounded to 16 KiB.

`GET`/`POST /v1/billing/budgets` list and create policies. `GET`/`PUT`/`DELETE
`/v1/billing/budgets/{id}` read, replace and tombstone a policy. Replacement and
deletion require its `expected_revision`, returning 409 for a stale edit.
Every mutation and its immutable actor/revision audit commit together. Policy
deletion retains that history, accessible through
`GET /v1/billing/budgets/{id}/revisions?after_revision=0&limit=100`; use
`next_revision` as the next cursor. A final full page can be followed by an empty
page. At most 128 nondeleted policies are allowed per account.

Activation currently returns 422 `financial_budget_activation_unavailable`
without writing policy intent. Save `enabled=false` to retain a draft. A policy
response separately reports `status`, `enforcement_ready` and readiness reasons.
An internal enabled intent whose integrations are unavailable reports
`unavailable`, never active protection. Cost reads, policy reads, updates and
deletion remain reachable while an account is suspended for billing recovery;
they do not change payment/security/deletion status or resume its workloads.

The Node and Python generated Billing clients expose the same operations,
revision conditions, retry headers and readiness fields. Revision history is
intent history; target stopping acknowledgements and action history are still
under implementation.

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

Refresh enriches known invoices. Provider history is discovered separately by
the backfill operation below; exports remain a projection of stored facts.

### Discover missing invoice history

Run one bounded provider page at a time:

```bash
gregale billing backfill-invoices
gregale billing backfill-invoices --cursor NEXT_CURSOR
```

`POST /v1/invoices/backfill` uses the same `usage:read` and session MFA gate as
invoice history. `--limit` accepts 1–25 provider records per page. The response
reports `scanned`, `imported`, and `skipped`; pass `next_cursor` to resume. The
cursor is bound to the active provider customer. Stripe, Paddle, and Polar
history is read with that provider-qualified customer identity, and every
returned document must name the same customer before import.

Import inserts missing documents only. An existing invoice or webhook record
with the same account, provider, and document ID is skipped unchanged. Each
page is one database transaction, so provider or persistence errors do not
partially import that page. Replaying a page is safe. Provider pagination is a
best-effort view rather than a frozen snapshot; `has_more: false` means the
provider reported no further page at that time. Repeat backfill later to find
newly surfaced or recently created documents.

Imports require EUR totals and tax, a supported invoice state, and an explicit
billing period. Rows without enough information are counted as skipped rather
than assigned guessed dates or amounts. Imported invoices have an explicit
unknown historical Gregale plan because provider history does not reliably
prove the plan that governed past usage. Credit-based proration for those rows
fails closed until the plan can be independently established. Refresh imported
documents with `gregale billing refresh-invoice INVOICE_ID` to retrieve their
provider line facts for FOCUS exports.

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

The next invoice steps are remaining price classifications and legal issuer
coverage, correction-document lineage, and conditional FX/PO fields. A
historical cost ledger with
service/resource identifiers, quantities, units, and price snapshots is then
needed for the **Cost and Usage** dataset. Current plan prices
cannot reliably reconstruct past list, contracted, or effective costs.

The mapping is based on the official
[FOCUS 1.4 Invoice Detail specification](https://focus.finops.org/docs/specification/v1-4/datasets/invoice-detail/).
`make focus-contract-check` checks the pinned upstream columns, exact
reconciliation, snapshot metadata, ownership, export/refresh bounds, provider
reads, lifecycle concurrency, and CLI operations.
