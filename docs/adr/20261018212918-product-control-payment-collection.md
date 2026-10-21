# Product Control payment collection through owner billing connections

Date: 2026-10-18
Status: Accepted for internal implementation (owner chose "owners bring their own provider" now, Paddle embedded billing later); slices 1–4 and the slice 5 billing details implemented (#5067); live provider qualification is `make product-control-provider-qualification`
Related: ADR-1701 (Product Control), ADR-1704 (billing review, finalization and invoice handoff), ADR-1502–1504 (statement invoice exports, billing runs, payment status), ADR-1507 (Paddle and Lago exports), ADR-1311 (provider collection), ADR-1313 (Stripe automatic tax), ADR-1315 (blocking consumers with overdue invoices)

## Context

Product Control (ADR-1701) charges a customer's platform tenant once per verified
successful Operation and closes each app/customer/UTC month into an immutable
statement (ADR-1704). Getting paid is still manual. The owner reviews and
finalizes every customer separately, creates an invoice in some other system, and
records its reference with `statements handoff`. Gregale never learns whether the
customer paid, so pausing a customer who has not paid is a manual
`customers assign --suspended`.

API monetization already solved this for request-priced statements without
Gregale touching money:

- an account connects its own Stripe, Paddle or Lago key (`account_billing_connections`);
- finalized statements export through one provider interface (`pkg/apid/statementinvoices/providers.go:41-56`);
- a worker polls each exported invoice's payment status for all three providers (ADR-1504, ADR-1507);
- billing runs bill every customer of a month in one run (ADR-1503);
- an opt-in per-app policy blocks API consumers whose invoices are overdue (ADR-1315).

The owner compared four ways to collect money for Product Control:

1. **Owners bring their own provider.** Gregale creates invoices in the owner's
   account and reads payment status back. Chosen now.
2. **Paddle embedded billing.** Gregale onboards each owner as a Paddle seller
   through the partner API, and Paddle is merchant of record per owner. Deferred.
3. **Stripe Connect, with Gregale as the platform.** Rejected for now.
4. **Building payments, or reselling owners' products through Gregale's own
   merchant-of-record account.** Rejected.

## Decision

Product Control statements become a third statement source for the existing
export, payment-status and billing-run machinery. Money moves only between the
owner's customer and the owner's provider. Gregale still never collects money,
holds funds, calculates tax or acts as merchant of record.

### 1. Statement source `product_control`

- Add `StatementSourceProductControl = "product_control"` beside
  `app_consumer` and `platform_tenant` (`pkg/state/statement_invoice_exports.go:22-23`).
  Widen `usage_statement_invoice_exports_source_chk` with an expand-only
  migration. `statement_id` is the Product Control statement document UUID.
- A source loader mirrors `tenantStatementInvoiceSource`
  (`pkg/apid/statementinvoices/sources.go:226-246`). It works as follows:
  - **Finalized statements only.** It accepts only a finalized Product Control
    statement. Credit notes are not exported in this decision; see §6.
  - **Lines.** It builds a `billing.StatementInvoice` with one line per review
    group: feature, catalog revision, plan and admitted unit price, multiplied
    by the count of charged successes. It reuses the existing millicent → minor
    unit rounding and rounding item. The statement's stored millicent totals do
    not change; rounding exists only on the provider invoice, as ADR-1704
    already requires.
  - **Customer.** It takes `externalRef` from the customer's platform tenant
    `ExternalRef`, so the provider customer defaults to the same reference
    platform-tenant exports use. An explicit `customer` in the request
    overrides it, with the same rules as other sources.
  - **Invoice reference.** It records ADR-1704's handoff through the `handoff`
    callback once the provider invoice exists. A document gets **either** a
    manual handoff **or** an export, never both. An existing manual handoff
    blocks export, and an export's invoice ID becomes the document's
    immutable handoff.
- Owner routes, behind `FAAS_PRODUCT_CONTROL_ENABLED`, use the same
  deploy-write/MFA policy as the other Product Control billing writes:
  `POST/GET /v1/apps/{slug}/product/billing/documents/{document}/invoice`.
  The CLI adds `gregale product statements export --document UUID --provider stripe|paddle|lago [--customer ID]`
  and `statements export-status`.
- **Connection settings.** Exports use the account's existing billing
  connection. Its collection mode (ADR-1311) and Stripe automatic tax
  (ADR-1313) apply unchanged and are fixed per export.
- **Currency.** The app's single catalog currency is checked against the
  provider's `currencyProblem`. For example, Paddle manual collection accepts
  only USD, EUR and GBP. An unsupported pair fails with 422 before any
  provider call.

### 2. Payment status

- The existing sync worker claims any completed export, so Product Control
  exports are polled with no worker change.
- **Owners** see payment status, amount remaining, due date and the provider
  link on each statement in the owner console and the CLI.
- **Customers** with `billing:read` see the status and the provider's hosted
  invoice or checkout URL in the portal and through `statements list --self`.
  The URL comes only from the provider. Gregale never renders payment forms.
- **Overdue** has the ADR-1504 meaning: `open`, past `due_at`, with an amount
  remaining.

### 3. Overdue holds for Product Control customers

- **Policy.** An opt-in per-app policy, `block_overdue_after_days` (0–365),
  mirrors ADR-1315. It is stored outside the catalog in its own table.
  - Why outside: catalogs are immutable, revision-pinned commercial terms,
    while overdue handling is collection policy that applies to every
    customer at once.
  - Owners set it through the owner console or
    `gregale product billing overdue-policy inspect|set|clear`.
  - Each change is audited.
- **Holds.** A recompute worker writes one hold row per (app, tenant) while a
  Product Control export for that customer is `open` or `uncollectible`, has an
  amount remaining, and is `days` past `due_at`. It uses the ADR-1315 cadence,
  advisory lock and audit events. It also pulls the held invoice's next
  payment sync forward, so a payment releases the hold quickly.
- **Enforcement.** Enforcement happens at Product Control admission, inside
  `prepareProductOperationTx` under the locks it already holds. A held customer's
  new start returns **402 `product_invoice_overdue`**, naming the due date and the
  hosted invoice URL. No start is consumed. The hold also applies to idempotent
  retries of work that was never admitted.
- **What a hold leaves alone:**
  - Work already admitted keeps running and settling.
  - Reading results, usage and billing stays available.
  - A hold is not the owner's `suspended` flag. The two are reported
    separately, and lifting one never lifts the other.
- **Release.** A hold lifts automatically when the invoice becomes paid,
  void or deleted, or when the owner clears the policy.

### 4. Month-close billing runs

- Billing runs (ADR-1503) gain Product Control items. An item has
  `SourceKind=product_control`, `SourceID` = tenant and `AppID` = app, for
  each assigned customer with receipts in the run's month.
- A new `billProductCustomer` step follows `billConsumer`
  (`pkg/apid/billingruns/customers.go:16`):
  1. It reviews the month inside the run.
  2. It holds the item with the review's problem
     (`product_billing_unresolved`, `product_billing_period_open`) when it
     cannot finalize.
  3. When the run asks to finalize, it finalizes with the hash it just
     reviewed.
  4. When the run asks to export, it exports.
- **Retries.** A changed review between steps is a transient problem, retried
  by the run. A held item is picked up by the next run once the owner waives
  or reconciles its receipts.
- **Scope.** Runs and schedules include Product Control items only when the
  feature flag is on and the app has a published catalog. Lock order stays
  account → app → tenant (ADR-1704).

### 5. Tax

Gregale's statement totals stay net of tax. The provider adds tax:

| Provider | Where tax comes from |
|---|---|
| Stripe | Stripe Tax with `automatic_tax` (ADR-1313) |
| Paddle | Paddle as merchant of record |
| Lago | Lago's own tax settings |

Gregale does not compute, file or display tax liability.

### 6. Deliberately excluded now

- **Credit notes** keep the manual ADR-1704 handoff.
  - Paddle and Lago reject negative documents (ADR-1507).
  - Stripe credit notes against an exported invoice are a separate follow-up.
- **Automatic collection depends on the provider:**
  - Stripe: `send`/`charge` (ADR-1311).
  - Lago: `charge`.
  - Paddle: exports stay drafts. Billing a Paddle transaction needs a customer
    address, which Product Control does not capture. A later "customer billing
    details" portal step could collect it and make Paddle exports billable.
- **No payment forms, card storage, refunds, failed-payment retries or tax
  logic in Gregale.**

### 7. Paddle embedded billing later (option B)

Option B becomes another connection kind in the same provider interface. Gregale
would create and verify the owner's Paddle seller account through Paddle's
partner API, and Paddle would be merchant of record for each owner. The
decisions above must not block it:

- Product Control stores no provider-specific fields.
- Customer mapping stays `externalRef`-based.
- The connection, not Product Control, decides who the merchant is.

Prerequisites before an ADR for B:

- Paddle partner eligibility for a Türkiye-based platform.
- Commercial terms and revenue share.
- Whether embedded sellers can use billed or automatically collected
  transactions.
- Written tax advice on Gregale's role.

## Consequences

- **What owners get.** They are paid in their own provider, under their own
  merchant terms, with one month-close run and automatic holds for unpaid
  customers. They no longer copy invoice references by hand.
- **No new financial role for Gregale.** No funds flow through Gregale, and it
  takes on no payment licence, PCI scope, merchant-of-record or tax liability.
  It earns no transaction fee, which is the trade-off for this.
- **The owner must open a provider account.** Paddle owners still finish each
  draft in Paddle until customer billing details exist. Stripe and Lago can
  collect without manual steps.
- **No double charging.** Product Control statements and request-priced
  statements stay separate documents with separate source kinds.
  ADR-1704's rule still applies: never charge the same work under both bases.
- **New limits** go in `pkg/api/limits.go`: the overdue days maximum, the
  recompute interval, and run batch bounds. Reuse the ADR-1315 constants'
  values unless measurements say otherwise.
- **Rollback.** Turning `FAAS_PRODUCT_CONTROL_ENABLED` off hides the new
  routes and stops Product Control holds from being enforced.
  - Existing exports keep syncing, because they are ordinary account exports.
  - Overdue holds stop applying, because no new Product Control starts are
    admitted while the flag is off.

## Rejected alternatives

- **Building payment processing.** It needs a payment or e-money licence, PCI
  DSS scope, fraud and chargeback handling, KYC/AML, and tax registrations.
- **Collecting through Gregale's own Polar or Paddle merchant account.**
  - Paddle's acceptable use policy excludes marketplaces where sellers other
    than the account holder reach customers.
  - Reselling owners' work would also make Gregale liable for tax on every sale.
- **Stripe Connect with Gregale as platform.** Stripe does not onboard
  Türkiye-based businesses, and self-serve cross-border payouts require a
  US, UK, EEA, Canadian or Swiss platform entity. Revisit only if Gregale
  incorporates in such a jurisdiction and wants a take rate.
- **Provider webhooks for payment status.** They would need each account to
  register endpoints in its own provider and still need reconciliation polling
  (ADR-1504). Polling is unchanged.
- **Putting the overdue policy in the catalog.** It would publish new catalog
  revisions for a collection setting and tie unpaid handling to pinned
  commercial terms.
- **Reusing `customers assign --suspended` for unpaid customers.** It would
  conflate an owner's commercial decision with a payment fact, and lifting
  one would silently lift the other.

## Implementation slices

Each slice is a separate PR behind `FAAS_PRODUCT_CONTROL_ENABLED`, with memory
and PostgreSQL contract tests, recorded-shape provider fakes, and expand-only
migrations.

1. **Export:**
   - source kind, check migration, source loader, owner routes, CLI;
   - handoff exclusivity;
   - currency checks;
   - the review-group → invoice-line mapping, with rounding tests.
2. **Payment visibility:** owner console, CLI and customer portal views of
   export payment status and hosted invoice links.
3. **Overdue holds:** policy table and API/CLI, recompute worker, admission
   check (402 `product_invoice_overdue`), portal explanation, audit events.
4. **Billing runs:** Product Control items and `billProductCustomer`, schedules,
   run notifications.
5. **Follow-ups:** Stripe credit-note export, and customer billing details so
   Paddle exports can be billed.

Live qualification needs real Stripe test-mode, Paddle sandbox and Lago accounts
plus a deployed Product Control pilot. Recorded fakes cannot establish any
provider's behaviour.
