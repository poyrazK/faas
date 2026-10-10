# API monetization

Charge the customers who call your API per request. Gregale identifies each
caller, meters their requests, prices them with versioned rate cards, and
produces auditable usage statements that you invoice through your own billing
system. Gregale does not collect payment from your customers.

API monetization is a preview. It is available on Hobby, Pro, and Scale; see
[Plans and pricing](plans.md) for consumer key limits.

## 1. Identify your consumers

A consumer is one of your customers. Give each one a stable reference from
your own system and issue it an API key:

```bash
gregale consumers create my-api --external-ref customer-42 --name "Customer 42"
gregale consumers key-create my-api CONSUMER_ID --name production --scopes read,write
```

The key secret is shown once. Your customer sends it as
`Authorization: Bearer ck_...`. Scopes limit methods: `read` allows `GET`,
while `write` and `admin` allow every method. To require a key on every
request, run `gregale app my-api --consumer-auth-mode required`. Revoking a
consumer makes all of its keys stop authenticating.

## 2. Meter requests

Every admitted request from an identified consumer records one billable unit,
whatever status your app returns. Two kinds of request record no billable unit:

- requests rejected by a [platform tenant](platform-tenants.md) request budget;
- requests that fail with a 5xx before your app sends any response, such as a
  failed wake or an exhausted capacity queue. These are platform failures, so
  they still appear in request and error counts but are not billed.

```bash
gregale consumers usage my-api CONSUMER_ID --since 2026-09-01T00:00:00Z
```

## 3. Set a price

A rate card is an immutable price version: a currency, a price per request in
millicents (100,000 millicents is 1.00), and the minute it takes effect.
Optionally include free requests each month:

```bash
gregale consumers rate-card-create my-api --currency EUR --price-millicents 25 --included-units 10000
```

This charges EUR 0.00025 per request after each consumer's first 10,000
requests in every UTC calendar month.

- **How the allowance is used.** A consumer's requests are counted in the
  order they happened, from the start of each month. Requests are free until
  the month's count passes the allowance of the card in effect at that moment.
- **Changing price or allowance.** Create a new rate card; earlier minutes keep
  their price. Once any card includes requests, new cards cannot take effect in
  the past.
- **Currency.** All of an app's cards use one currency.

### Volume tiers

For volume discounts, give the card a graduated ladder instead of a single
price. Each step lists the monthly position it runs up to and its price; the
last step is `inf`:

```bash
gregale consumers rate-card-create my-api --currency EUR \
  --tier 10000:0 --tier 1000000:25 --tier inf:10
```

Each consumer's first 10,000 requests in a UTC calendar month are free.
Requests from 10,000 to 1,000,000 cost EUR 0.00025, and every request after
that costs EUR 0.0001.

- **Graduated pricing.** Each request is priced by the step it falls in. The
  month's total never re-prices earlier requests.
- **Ladder rules.** Bounds must increase, only the last step is unbounded, and
  only the first step may be free.
- **Whole-month statements.** Statements for periods priced by a tiered card
  must cover exactly one UTC calendar month, so use `--month`.

### Route weights

When some endpoints cost much more to serve, weight them. A request on a
weighted route counts as that many units, and every other request counts as
one:

```bash
gregale consumers rate-card-create my-api --currency EUR --price-millicents 25 \
  --weight "POST /generate=20" --weight "GET /reports/{id}=5"
```

- **What gets weighted.** Weighted units are what free requests, tiers, and
  statements count. With the card above, one `POST /generate` call uses 20 of
  a consumer's free requests and is billed as 20 units.
- **Route labels.** Labels are an HTTP method and a path template, such as
  `GET /items/{id}`. They match your app's declared route templates, or the
  shapes Gregale infers from paths when none is declared. Up to 50 routes per
  card, each weighted 1 to 1000.
- **Unlisted routes.** Requests on unlisted routes, and on routes past an
  app's first 50 labels, count as one unit.
- **No backdating.** Like free requests, weighted cards cannot take effect in
  the past.

`gregale consumers quote` estimates charges for a window at current prices
without creating anything.

### Plans

Package prices and limits as named plans, such as a capped free tier and a
paid tier:

```bash
gregale consumers plan-create my-api --name free --max-requests-per-minute 60 --max-units-per-month 1000
gregale consumers rate-card-create my-api --plan free --currency EUR --price-millicents 0
gregale consumers plan-create my-api --name pro --max-requests-per-minute 600
gregale consumers rate-card-create my-api --plan pro --currency EUR --tier 100000:0 --tier inf:20
gregale consumers set-plan my-api CONSUMER_ID --plan free
```

- **What a plan contains.** A plan has its own rate-card history, created
  with `--plan`. Rate cards without a plan form the app's default plan, which
  prices every consumer you have not assigned.
- **Limits.** A plan limits requests per minute and weighted units per UTC
  month, where `0` means unlimited.
  - Gregale enforces both at the edge across all gateway replicas.
  - Over a limit, your consumer gets `429` with `Retry-After` and
    `x-faas-rate-limit-scope: consumer-plan-minute` or
    `consumer-plan-month`.
  - Rejected requests are not billed.
  - Limit changes apply within 15 seconds.
- **Changing plans.** `set-plan` moves a consumer from the next minute, or from
  `--effective-from`, which cannot be in the past. Statements price each minute
  with the plan in force then. Free requests and tiers keep counting through
  the month, so an upgrade mid-month does not reset them.
- **Plan prices first.** The target plan needs a rate card in force before
  consumers can move onto it. Use `--plan default` to return a consumer to the
  default plan, and `plan-history` to review their changes.
- **Cap versus bill.** The monthly cap counts requests when they are admitted,
  so a request that later fails on Gregale's side still uses cap but is not
  billed.

## 4. Bill with statements

A statement snapshots one consumer's usage for a period and prices every
minute with the rate card that was effective then:

```bash
gregale consumers statement-draft my-api CONSUMER_ID --month 2026-09
gregale consumers statement-finalize my-api CONSUMER_ID STATEMENT_ID
gregale consumers statement-handoff my-api CONSUMER_ID STATEMENT_ID --invoice-id INV-1001
```

- **Draft.** A draft can be recreated freely. If usage or prices changed, the
  old draft becomes `superseded` and a new revision replaces it; unchanged
  drafts replay.
- **Finalize.** Finalizing freezes the statement. It fails while any usage is
  unpriced; add a rate card and draft again first.
- **Handoff.** Handing off records your external invoice ID. Each finalized
  revision can be handed off once, and an invoice ID cannot be reused.
- **Late usage.** Drafting the same period again after finalization creates an
  adjustment revision that holds only what was not billed yet:
  - new requests;
  - requests that became chargeable because late usage earlier in the month
    used up the free allowance sooner;
  - with volume tiers, re-rated minutes. Late requests push later requests
    into a cheaper step, so a line can be negative, but an adjustment's total
    never is.

  Invoice the adjustment on its own; never re-invoice earlier revisions.
- **Webhooks.** A `usage_statement.finalized` webhook can trigger your
  invoicing.

Statements cannot overlap: once a period is handed off, a different period
covering the same minutes cannot be handed off for that consumer.

### Check usage before invoicing

The completeness check compares a consumer's billed requests with Gregale's
request telemetry, hour by hour:

```bash
gregale consumers completeness my-api CONSUMER_ID --month 2026-09
```

Both sides count only successful requests, so unbilled errors, rate-limit
rejections and platform failures never look like gaps. The result is one of:

| Status | Meaning |
|---|---|
| `verified` | Telemetry confirms every billed request and saw none that is missing. |
| `partial` | No gaps, but telemetry is sampled and confirms only part of the billed requests. |
| `gaps_detected` | Telemetry saw successful requests the ledger never billed. `missing_requests` is a lower bound. |
| `unverifiable` | Telemetry has no data for the period: it is disabled, sampled out or expired. |

Only whole UTC hours that ended at least 10 minutes ago and fall within
telemetry's 14-day retention are checked. Run it before you finalize a
month-end statement. `statement-draft` and `statement-finalize` print a
warning when the check finds gaps. The check is advisory: it never blocks
finalizing and stores nothing.

## Customers across several apps

To bill one customer across several apps, link their consumers to a
[platform tenant](platform-tenants.md). You can then create cross-app
statements and a customer-wide rate card with
`gregale platform-tenants statement-draft` and `rate-card-create`. Tenant rate
cards do not support free requests, tiers, route weights, or plans yet. A
cross-app statement cannot price an app whose rate cards use them, so bill
those consumers with app statements.

## Limits

- Units are requests, optionally weighted by route. Pricing by measured
  compute time or bytes is not available.
- Tiers are graduated. Pricing every request in the month at the price of the
  step the month ends in is not available.
- Usage is recorded when a request finishes. If the gateway crashes before the
  record is written to disk, that request can be lost. The completeness check
  finds such losses only while request telemetry still holds the period
  (14 days) and only where telemetry was not sampled. Reconcile with your own
  records before closing a high-value invoice.
- Gregale records the handoff to your billing system but never charges your
  customers.

See [ADR-933](adr/933-app-consumer-statement-revisions-and-platform-failure-billing.md),
[ADR-934](adr/934-api-consumer-monthly-allowances.md),
[ADR-935](adr/935-api-consumer-graduated-tiers.md),
[ADR-936](adr/936-api-consumer-route-weights.md),
[ADR-937](adr/937-api-consumer-plans.md), and
[ADR-938](adr/938-api-consumer-usage-completeness.md) for the billing rules.
