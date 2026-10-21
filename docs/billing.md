# Billing

Gregale bills the account that owns an app. Plan prices and included capacity are generated from the platform limits table in [Plans and pricing](plans.md).

```bash
gregale usage --month 2026-09
gregale billing status
gregale billing portal
```

Usage is measured in GB-RAM-hours for running app instances. The plan also sets deployed-app, developer-app, concurrency, storage-layer, and idle-timeout limits. Apps parked by scale-to-zero, and function instances held frozen between requests, do not accrue running-instance usage; storage and other explicitly metered services remain separate line items.

Free accounts have a zero monthly charge and are subject to the published limits. Paid plans are billed monthly through the account billing portal. Treat the portal as the source of truth for invoices, tax, payment methods, credits, and failed-payment recovery.

## Spend cap

A spend cap is a hard ceiling on the overage an account is billed in a UTC
calendar month. The included GB-RAM-hours are not counted against it.

```bash
gregale overage-cap 500      # cap overage at $5.00 this month
gregale overage-cap --clear  # remove the cap
```

`POST /v1/account/overage-cap` with `{"overage_cap_cents": 500}` (or `null`
to clear) does the same. A cap of 0 is a pause switch: it counts as reached
immediately, so the account's workloads stop even within the included
allowance.

When the month's overage reaches the cap:

- usage is billed exactly up to the cap and no further. Raising the cap later
  does not bill the usage the cap excluded;
- running instances, minimum-instance floors, service replicas, workers and
  warm pools are parked within one scheduler tick;
- new wakes are refused with HTTP 402 `admission_refused`; the problem body
  carries the observed overage and the cap;
- queued job tasks wait in the queue; they are not failed;
- object storage, which is billed once the month closes, is billed only up
  to what the cap still allows after the month's compute and egress overage.
  Object storage keeps serving within its plan limits.

Raising or clearing the cap, or the start of the next month, lets workloads
wake again on the next request or scheduler tick. If you change plans during
a month, the month keeps the largest allowance any of its plans granted.

## Retained usage costs and forecasts

This financial visibility preview reports supported usage costs and budget
policies. Enabled monitored notification policies send spending alerts; disabled
drafts do not. Monitored preview stopping and strict app compute are local
candidates pending native acceptance; see their sections below.
Coverage and unavailable bill components are reported explicitly.

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
Recorded price activations define the retained plan for each sampled interval;
closing the previous minute after a plan change keeps that minute's earlier
contract. Completed and deleted jobs retain their final resident seconds.

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

For a single application, `gregale app <slug> costs [--month YYYY-MM]` shows
retained compute and egress allocations whose stable app ID matches that app.
The app detail dashboard lets customers select a UTC month, and shows source
coverage and the largest workload allocations. Closed months compare as full
months; the current month compares with the same elapsed part of the previous
month, capped at its end. Comparisons appear only when both periods have
complete, fresh, priced data for every meter. Costs include the app's allocated
share of the account's included usage allowance. The on-demand six month trend
shows per-meter bars only for complete, fresh, priced periods; incomplete
periods include coverage details. The **App costs** dashboard page ranks apps
for a selected month by retained compute and egress costs. CSV downloads include
selected-period meter totals, workload allocations, and source coverage; a
separate six month trend export includes those rows for each month and marks unavailable months.
Separately attributed jobs,
unallocated usage, account-wide charges, and missing bill components are not
included in the app subtotal. This app-scoped view does not provide a forecast
or invoice total.

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
  "currency": "USD",
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

The limit above is $10.00. Available scopes are account, project,
environment, application, and job; resource scopes use their stable UUID.
Preview identity comes from Gregale's persisted preview metadata. Environment
membership uses the deployment’s retained runtime environment identity. Historical costs retain
older identities even when the current target list changes.

Responses explain the difference between notifying, rejecting new traffic,
stopping previews, suspending background work, and suspending every workload in
a scope. Rejecting traffic leaves running compute able to spend. Broad selective
responses leave other workloads able to spend. Net usage applies the shared
account allowance once; gross usage measures cost before that allowance.
Strict resource scopes require gross compute, and a broad strict policy must
stop every covered workload.

Notification-only policies report `enforcement_ready: false`. Monitored
compute-only preview stopping reports integration readiness separately from
actual per-target stopping acknowledgements. The strict app compute candidate
reports conservative commitments and funding separately. Other stopping actions
and strict scopes remain unavailable. A successful preview does
not activate a spending limit. Monitored responses can exceed a threshold while
usage arrives or workloads drain. Full-invoice hard limits are not implied by
compute controls.

## Budget policy drafts and audit

The console's Usage page creates account or app compute notification policies,
shows monitoring health and threshold delivery state, and enables or disables
notification policies at their current revision. Other scopes and existing
policy specifications remain accessible through the API and CLI. USD text is
parsed to exact millicents. Disabled drafts do not stop usage or send alerts.

```bash
# Use enabled=false for unsupported policy drafts; see supported modes below.
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

`mode=monitored` with `action=notify`, or compute-only `action=stop_previews`,
accepts `enabled=true`. The strict app compute contract below also supports
activation when its readiness checks pass. Other enabled
responses return 422 `financial_budget_activation_unavailable` without changing
policy intent. Save `enabled=false` to retain those drafts. A notification policy
reports `status=active`, `notification_ready=true` and `enforcement_ready=false`.
It never claims that workloads stopped or that spending is capped. Unsupported
legacy enabled intent reports `unavailable`. Cost reads, policy reads, updates and
deletion remain reachable while an account is suspended for billing recovery;
they do not change payment/security/deletion status or resume its workloads.

The Node and Python generated Billing clients expose the same operations,
revision conditions, retry headers and readiness fields. Revision history is
intent history; `runtime.stops` separately retains threshold decisions and
current scheduler acknowledgements.

### Budget alerts on Slack, PagerDuty and email channels

A budget's threshold alerts always go to the account email. They can also go to
up to five of the account's notification channels (the same Slack, PagerDuty
and email channels alert rules use;
[ADR 20261019193500](adr/20261019193500-budget-alert-channels.md)):

```bash
gregale channels list                                         # find channel IDs
gregale billing budgets channels --set CHANNEL_ID,CHANNEL_ID POLICY_ID
gregale billing budgets channels POLICY_ID                    # last delivery per channel
gregale billing budgets channels --clear POLICY_ID
```

The REST form is `GET`/`PUT /v1/billing/budgets/{id}/channels` with
`{"channel_ids": [...]}`; `PUT` needs `admin`, session MFA and an
`Idempotency-Key`. Changing channels does not change the policy revision.

Each threshold is sent once to each channel. A failed send is retried with
backoff (1 minute, doubling to 1 hour) and given up after 12 attempts; the
channel shows the last error. A channel added mid-month receives a threshold
already crossed this month at the next evaluation. Removing a channel pauses
its pending alerts, and deleting it discards them. On PagerDuty, every threshold
of one budget in one month updates a single incident: `warning` below the
limit, `error` at it. Amounts are the selected usage meters, not the invoice.

### Projected overruns

Each evaluation also projects the budget to the end of the UTC month at this
month's usage rate (`runtime.evaluation.projected_millicents`; `gregale billing
budgets get` prints "Projected month end"). When the projection reaches the
limit while known usage is still below it, the account email and the bound
channels get one "projected to exceed its limit" alert for the month. It
shows the projected amount, the limit and the usage so far. It is an estimate,
not an invoice, and it never stops a workload.

A projection needs complete, fresh evidence for every selected meter and at
least a day of usage this month. Otherwise `projection_reason` says why it is
unavailable (for example `compute:insufficient_history`). Editing the budget
starts a new revision, so a still-projected overrun can alert again.

### Object storage in budgets

A monitored account budget can include object storage by adding
`object_storage` to `meters`, for example `["compute", "object_storage"]`.
Object storage cannot be selected on app, job, project or environment budgets
(buckets carry no such attribution), or on strict budgets
([ADR 20261020160000](adr/20261020160000-object-storage-in-budgets.md)).

The amount is the live object-storage estimate: this month's stored
GiB-months, requests and egress, priced with the same rates and plan
allowance as `gregale usage object-storage` and month close. Gross is before the
allowance and net is after. apid refreshes it every five minutes, and an
estimate more than 15 minutes old counts as stale. `GET /v1/billing/costs`
shows it as the `object_storage` meter. Threshold and projected-overrun alerts
count it like compute and egress. The invoice line still comes from month
close, which also applies the overage cap; the budget counts the uncapped
estimate.

## Active spending notifications

For example, save this specification to `budget.json` and run
`gregale billing budgets create --file budget.json --key monthly-compute --json`:

```json
{
  "name": "Monthly compute",
  "scope": {"kind": "account"},
  "currency": "USD",
  "meters": ["compute"],
  "basis": "net_usage",
  "limit_millicents": 1000000,
  "notify_millicents": [800000],
  "mode": "monitored",
  "action": "notify",
  "drain_seconds": 0,
  "resume_rule": "next_period",
  "enabled": true
}
```

This sends spending alerts to the account email at USD 8 and USD 10 of known
recorded compute usage. The limit is always a notification threshold too, with
identical amounts deduplicated. All existing account/project/environment/app/job
scopes and the supported compute/interface-egress meters are accepted by the
API and CLI. This does not include subscription, tax, managed-resource or other
missing bill components.

`meterd` evaluates once per sampling interval, independently of provider billing
and email delivery. The evaluation and its threshold events commit together,
guarded by the policy revision. Events are unique per policy revision, threshold
amount and UTC usage month. Usage replay and downward corrections followed by a
recrossing do not resend an event. Editing or re-enabling creates a new revision
and evaluates the existing month's spending again; it does not reset spending.
Deleted or superseded policy revisions cannot acquire new delivery claims. An
alert already claimed for delivery when the policy changes may still arrive.

A fresh **known subtotal** can cross a threshold even with partial coverage;
the email and runtime explicitly label it partial. Stale evidence cannot create
new events. Failed evaluations omit `known_millicents`; they never substitute
zero. Missing scope attribution remains a coverage gap. An amount below the
threshold with partial or unavailable evidence does not establish that total
spending is below the budget.

Policy reads include `runtime.evaluation` and the current revision/month's
`runtime.alerts`. They expose evidence and evaluation timestamps, coverage,
known usage, threshold events, attempts, retry time and delivery state. No
evaluation means the revision has not yet been evaluated in the current month.
API reads mark old monitor observations stale even if the worker has stopped.
`gregale billing budgets get ID` prints this health; `--json` returns the details.

An independent delivery worker claims durable events with fenced leases. Failed
attempts retry with capped exponential backoff; a process restart recovers expired
claims. The existing configured mail pipeline, suppression rules and transport
apply. `delivered` means the pipeline returned success, not confirmed inbox
receipt. Log transport is for operator-selected development/diagnostic delivery.
Every attempt uses the same message ID. Resend deduplicates within its provider
window; Postmark does not support idempotency keys, so delivery is **at least
once**, including the unavoidable send-success/commit-failure boundary.

The workers are visible separately as `financial_budget_evaluation` and
`financial_budget_delivery` in meterd health. Email failure cannot block sampling.
Deploy the additive monitoring migration before activating policies, run matching
`apid` and `meterd` versions, and verify a threshold and one retry with an isolated
account before promoting the capability. This release does not add scheduler,
gateway or VM lifecycle enforcement.

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

Imports require USD totals and tax, a supported invoice state, and an explicit
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

## Monitored preview stopping

Set `mode=monitored`, `action=stop_previews`, `meters=[compute]`, a drain interval
from 0 to 300 seconds, and an explicit `manual` or `next_period` resume rule.
Only persisted preview workloads are selected; production names, environment
names and absence of protection do not establish preview identity.

At a fresh recorded threshold, meterd latches the decision with its evidence and
notification outbox. New warm/cold requests, mesh HTTP/TCP calls, queued request
claims and command dispatch are blocked. Existing previews drain until the
configured deadline; the owning scheduler then destroys their resident VMs via
vmmd, retaining deployment snapshots/rootfs for the next allowed cold wake.
Command VMs carry retained node/lease ownership and preparation deadlines too.

Inspect `runtime.stops[].targets`: `draining` means resident resources remain;
`enforced` requires confirmed teardown and durable settlement; `failed` requires
retry/recovery. Stale scheduler observations become `unavailable`. A decision or
email alone is not proof of shutdown. `gregale billing budgets get ID` and the
console expose these states, including a manual hold from a previous month.

Corrections and unavailable costs never automatically release a hold. Disabling,
editing or deleting a policy releases that policy revision only; other budget,
payment, security and user holds remain. An already-started teardown may finish.
`next_period` releases at the next UTC month; `manual` persists until an explicit
policy mutation. Re-enabling evaluates existing monthly spending again.

This is a monitored response: usage ingestion, scheduler sweeps, draining and
teardown can exceed the threshold. Production and uncovered meters can continue
spending. It does not establish a strict compute or full-invoice ceiling.


## Strict app compute candidate

This implementation is local; native KVM acceptance and release review remain
required. Strict budgets are released per account through the `strict_budgets`
platform flag. Until it includes your account, creating or enabling one returns
422 `financial_budget_activation_unavailable` ("Strict budgets not released").

A project can be capped the same way with `scope={kind:project,id:PROJECT_ID}`:
one limit shared by every app in the project, and when it is reached every app
in the project is held. Jobs are not part of a project, so they need their own
job caps. Activation needs every app in the project to be idle
([ADR-1750, amendment 1](adr/1750-strict-app-compute-reservations.md#amendment-1-2026-10-20-strict-project-caps)).

An environment can be capped with `scope={kind:environment,id:ENVIRONMENT_ID}`:
one limit shared by every deployment that runs in the environment, across the
project's apps. When it is reached, only that environment's deployments are
held; the same apps keep serving other environments. A deployment without a
recorded environment counts toward the project's environment of the same name.
Capacity floors are app-wide, so an environment cap neither counts nor waives
them; use an app or project cap to bound them. Activation needs every
deployment in the environment to be idle
([ADR-1750, amendment 2](adr/1750-strict-app-compute-reservations.md#amendment-2-2026-10-21-strict-environment-caps)).

Configure `scope={kind:app,id:APP_ID}`, `mode=strict`,
`basis=gross_usage`, `meters=[compute]`, `action=suspend_workloads`,
`drain_seconds=0`, and `notify_millicents=[]`. A separate monitored policy can
send spending alerts. Subscription, storage, network egress, taxes, builds and
independent job resources are outside this app compute limit.

Activation requires a quiet app, settled prior VM ownership, complete retained
closed-minute accounting from app creation or UTC month start, and matching
native compute owners on all active nodes. Unsupported dedicated qualification
restore/job paths fail closed. Live migration cannot duplicate an active funded
instance identity; drain it before moving that workload.

Every VM reserves startup/execution and teardown funding before allocation.
Renewal failure preserves a local deadline; no timeout releases the money.
Shutdown and financial settlement must be confirmed. The API's `runtime.compute`
and `gregale billing budgets get POLICY_ID` show committed, reserved and remaining
millicents, unconfirmed VM attempts and durable holds. Commitments use a paid-rate
upper bound, include startup, and round up per funded minute; they can exceed
known billed compute. Cost evidence retains its own freshness/coverage status.

Changing/deleting/replacing a policy never resets the app/month book. A new
limit must cover existing funding. Manual holds cross UTC months until their
policy changes; next-period holds reset at the next UTC month. Independent
payment, security and user holds still apply.

Execution funding ends before the UTC reset with thirty seconds for teardown.
Work may pause at that boundary until earlier attempts settle and the new
month's accounting and funding are ready.

The meter charges only funded windows, even when instance rows are stale after
local shutdown. Gregale absorbs unfunded teardown tail. Strict budgets preempt
capacity floors; the book waives synthetic floor charges for that UTC month,
including replay after policy deletion. This bounds supported customer compute
charges, rather than promising a ceiling on host cost or the complete invoice.
See the [decision](adr/1750-strict-app-compute-reservations.md) and
[native acceptance procedure](runbooks/financial-strict-compute-acceptance.md).

### Currency and retained funding

New budgets and strict funding use USD, matching ADR-1430. Historical EUR
invoices, price snapshots and budget revisions are retained without conversion.
A period containing incompatible historical contracts cannot activate strict
funding. The forward-only USD funding migration refuses pre-release books or
enabled non-USD policies; those require explicit operator repair rather than
reinterpreting retained balances. Apply the complete reviewed migration set
before enabling the matching daemon graph.
