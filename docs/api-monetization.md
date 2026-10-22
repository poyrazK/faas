# API monetization

Charge the customers who call your API per request. Gregale identifies each
caller, meters their requests, prices them with versioned rate cards, and
produces auditable usage statements that you invoice through your own billing
system. Gregale does not collect payment from your customers.

API monetization is not released (ADR-20261018090210). It stays off, with
no customer-facing surface, until an operator sets the `api_monetization_enabled`
runtime setting; `FAAS_API_MONETIZATION_ENABLED=1` is its bootstrap value for
apid and gateways and shows its commands in the CLI. Once released it is
available on Hobby, Pro, and Scale; see [Plans and pricing](plans.md) for
consumer key limits.

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
request, run `gregale app my-api --consumer-auth-mode required`.
Gregale reserves only `Bearer ck_...`: in the default optional mode, any
other `Authorization` value (`Basic ...`, `Token ...`, or a Bearer token
without the `ck_` prefix) reaches your app unchanged. Required mode rejects
anything but a valid consumer key, and an app with `require_authn` on still
needs a Gregale API token. Revoking a
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

To charge every request in the month at the price of the step the month
reaches, add `--tier-mode volume`:

```bash
gregale consumers rate-card-create my-api --currency EUR \
  --tier 10000:0 --tier 1000000:25 --tier inf:10 --tier-mode volume
```

A consumer who stays under 10,000 requests pays nothing. One who makes
50,000 pays EUR 0.00025 for every one of them, first requests included.

- **Volume pricing.** Each request costs the price of the step the consumer's
  month total falls in, the first step whose bound the total doesn't exceed.
  Quotes during the month use the total so far.
- **Late usage.** Late usage that keeps the month in its step, or moves it to
  a step with a higher total, is billed as an adjustment. If late usage
  arrives after the month is finalized and would move it to a cheaper step,
  the adjustment bills it at the step already billed, because Gregale never
  issues credits.

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

### Compute time and response size

Charge for what a request costs you instead of counting requests. Set the
card's `--unit`:

```bash
gregale consumers rate-card-create my-api --currency EUR --price-millicents 50 --unit compute_second
gregale consumers rate-card-create my-api --currency EUR --price-millicents 900 --unit response_megabyte
```

- **Compute time.** A request's compute time is the execution time the
  runtime reports. For runtimes that report none, it is the time from
  forwarding the request to its response completing; a wake or cold start
  is never counted.
- **Response size.** The bytes of the response body sent to the client; a
  megabyte is 1,000,000 bytes.
- **Rounding.** Each consumer's billable requests are added up per minute
  and rounded to the nearest whole second or megabyte, so each minute is
  off by at most half a unit.
- **Billable requests only.** Requests that fail through no fault of the app
  are not measured, like any other unbillable request.
- **Works with the rest.** Free units and tiers count seconds or megabytes,
  and plans can price either. Route weights apply to request cards only. A
  compute or response-size card cannot take effect in the past.
- **Plan limits still count requests.** A plan's monthly cap and per-minute
  limit are enforced at the edge in requests, whatever the card's unit.

### Units your app counts

Charge for what only your app knows, such as tokens or images. Report the
units in a `Gregale-Usage` response header, and price them with a card whose
unit is `app:<meter>`:

```http
Gregale-Usage: input_tokens=1200, output_tokens=332
```

```bash
gregale consumers rate-card-create my-api --currency EUR --price-millicents 1 --unit app:output_tokens --included-units 100000
```

- **Meters.** Names are lowercase letters, digits and underscores, up to 32
  characters. A request reports at most 8 meters, each up to 10^12 units.
  Repeated meters add up and malformed pairs are ignored.
- **Streaming.** On a streamed response, send `Gregale-Usage` as a trailer
  so units counted while streaming are included. A response with a
  `Content-Length` must use the header.
- **Private.** Gregale removes the header or trailer before the response
  reaches your customer.
- **What counts.** Only billable consumer requests are counted. Anonymous
  traffic and requests your customer isn't billed for are ignored.

### Several units at once

Cards of different units price side by side. Add a compute or token card and
your request card keeps charging too:

```bash
gregale consumers rate-card-create my-api --currency EUR --price-millicents 20
gregale consumers rate-card-create my-api --currency EUR --price-millicents 50 --unit compute_second
```

- **Separate counts.** Each unit has its own free units, tiers and volume
  steps. A statement shows one line per unit for each minute.
- **Stopping a unit.** To stop charging a unit, add a card for it at price 0.
- **Customer-wide cards** price several units the same way (see
  [Customers across several apps](#customers-across-several-apps)).

### Monthly base fee and minimum spend

Add a subscription to usage pricing, or commit customers to a minimum:

```bash
gregale consumers rate-card-create my-api --currency EUR --price-millicents 50 \
  --included-units 100000 --base-fee-millicents 4900000 --minimum-millicents 10000000
```

That is EUR 49.00 a month including 100,000 requests, then EUR 0.0005 per
request, and at least EUR 100.00 of usage a month.

- **Which card.** The card in force when a consumer's month starts (the 1st
  at 00:00 UTC, or when the consumer was created) sets that month's fee and
  minimum, unless cards prorate (below).
- **Proration.** By default a card charges whole months. Add
  `--prorate-monthly-fees` to charge each card's fee and minimum for the
  share of the month it is in force:
  - a consumer created mid-month pays from its creation;
  - after a plan change or a new card mid-month, the old card charges until
    the change and the new one from it, each minimum covered by the usage that
    card priced. A new card that doesn't prorate starts charging next month;
  - a consumer revoked mid-month pays until the revocation, unless a
    statement of that month was priced before the revocation and finalized:
    finalized fees are never reduced. Finalize monthly statements after the
    month ends, as billing runs do.

  A share that shrinks after its minimum was finalized never lowers that
  minimum; only usage billed later does.
- **Minimum.** When usage costs less than the minimum, the statement adds a
  top-up for the difference. Late usage lowers the top-up instead of adding
  to the total.
- **Every consumer pays.** Billing runs include consumers without usage in
  apps whose cards charge fees.
- **Whole months.** Statements priced by such a card must cover one
  calendar month.

### Free trials

Give a new consumer a free trial: its usage is priced at zero and doesn't use
up included units.

```bash
gregale consumers create my-api --external-ref acme --name Acme --trial-days 14
gregale consumers trial-set my-api CONSUMER_ID --days 30
gregale consumers trial-end my-api CONSUMER_ID
```

- **One trial.** A consumer gets one trial. It starts at the next minute and
  can be extended or ended early, never backdated.
- **Fees.** A month that starts during the trial has no base fee or minimum.
- **Limits still apply.** Plan limits are enforced during a trial.

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

### Usage alerts

Warn consumers before they reach their monthly cap. Give a plan up to five
percentages of its monthly unit limit:

```bash
gregale consumers plan-create my-api --name free --max-units-per-month 1000 --alert-at 80,100
gregale consumers plan-update my-api --plan free --alert-at 50,90
gregale consumers usage-alerts my-api CONSUMER_ID
```

- **When it fires.** When a consumer's monthly usage reaches a threshold, Gregale
  sends a `consumer.usage_threshold` app webhook. It fires once per consumer,
  threshold and UTC month.
- **Payload.** It carries the consumer's `external_ref`, the plan, the
  threshold, the limit and the units used, so you can email your customer or
  offer an upgrade.
- **At 100%.** The 100% alert also fires when a request is refused for the
  month before usage lands exactly on the limit, for example a weighted
  request larger than what is left.
- **Requirements.** Alerts need a monthly unit limit; a plan without one has
  nothing to measure. Subscribe a webhook to `consumer.usage_threshold`, or to
  all events, to receive them.
- **Missed deliveries.** `usage-alerts` lists the last 100 crossings, so you
  can catch up on webhooks you missed.
- **Changing thresholds.** `plan-update --alert-at none` removes alerts.
  Changing thresholds mid-month does not re-send alerts already recorded.

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

### Send statements to Stripe, Paddle or Lago

Turn finalized statements into invoices in your own billing provider instead
of retyping totals. Connect each provider once; the key is read from stdin
and sealed at rest:

```bash
gregale account billing-connection connect stripe < stripe-key.txt
gregale account billing-connection connect paddle < paddle-key.txt
gregale account billing-connection connect lago --api-url https://api.eu.getlago.com < lago-key.txt
gregale consumers statement-export my-api CONSUMER_ID STATEMENT_ID --provider paddle
gregale platform-tenants statement-export --id TENANT_ID --statement-id STATEMENT_ID
```

- **Providers.** `--provider` is `stripe` (the default), `paddle` or `lago`:

  | Provider | Key | What the export creates |
  |---|---|---|
  | Stripe | A restricted key with read and write access to invoices and invoice items. | A draft invoice, unless the connection collects. |
  | Paddle | An API key with transaction access and read access to customers. | A draft, manually collected transaction with 30-day payment terms, unless the connection collects. |
  | Lago | An API key. `--api-url` selects the EU region or a self-hosted Lago. | A one-off invoice on the `gregale_usage` add-on. Lago issues it at once, and charges it only if the connection collects. |

- **Customer.** Use `--customer`, or set the consumer's or tenant's
  `external_ref` to the provider's customer. That is a Stripe `cus_…` ID, a
  Paddle `ctm_…` ID, or the Lago external customer ID.
- **Invoice lines.** Each rate card and unit price becomes one invoice item,
  and free requests show as no-charge items. If rounding each item to the
  currency's smallest unit would change the total, a rounding item keeps the
  invoice equal to the statement total rounded the same way. Paddle and Lago reject negative
  amounts. They receive one summary item instead when an adjustment has a
  negative line, or when a statement has more than 100 lines.
- **Drafts by default.** Unless the connection collects, Gregale never
  finalizes or sends a Stripe or Paddle invoice. It leaves other pending items
  on the customer alone. Review the draft and send it from the provider. Lago
  has no draft one-off invoices, so a Lago export is issued right away.
  Whether Lago emails it depends on your Lago settings.
- **Collecting.** To have your provider collect each export, connect with
  `--collection`:

  ```bash
  gregale account billing-connection connect stripe --collection charge < stripe-key.txt
  gregale account billing-connection connect stripe --collection send --days-until-due 14 < stripe-key.txt
  gregale account billing-connection connect paddle --collection send --days-until-due 14 < paddle-key.txt
  gregale account billing-connection connect lago --collection charge < lago-key.txt
  ```

  | Collection | Stripe | Lago | Paddle |
  |---|---|---|---|
  | `none` (default) | Draft invoice | Issued, not charged | Draft transaction |
  | `send` | Finalized and emailed, due after `--days-until-due` (default 30) | Not available | Billed with the customer's newest address and emailed, due after `--days-until-due` |
  | `charge` | Finalized and charged to the customer's default payment method | Charged through Lago's payment provider | Not available |

  A Paddle invoice billed with `send` also carries the customer's most recently
  updated active business, and so its tax identifier, when it has one
  ([ADR-20261020120000](adr/20261020120000-product-control-customer-billing-details.md)).
  Product Control customers can enter their address and business in the
  portal; for other customers, add them in Paddle.

  Charges, retries and emails follow your own provider settings. The money
  never passes through Gregale. Each export keeps the mode it started with.
  With `send` or `charge`, an export or billing run reaches your customers
  directly, so check prices and customer IDs on a draft first. Paddle can't
  charge an invoice to a saved card, and needs an address on the customer
  before it bills one: without one the export stops with the transaction
  kept as a draft, and exporting again after you add the address bills it.
- **Tax.** Connect Stripe with `--automatic-tax` to have Stripe Tax add tax
  to each exported invoice. It needs Stripe Tax set up and customer
  addresses. Gregale's statement totals stay net of tax.
- **Handoff.** The export records the statement's handoff with the provider's
  invoice ID, so the usual rule that a revision is invoiced once applies. A
  statement exports to one provider only. The export reserves the handoff
  before it calls the provider, so while it runs, a statement covering the
  same usage, such as a customer statement that includes the consumer, gets
  `409` instead of being invoiced too. If the provider refuses the invoice,
  the reservation is released.
- **Retries.** If the provider fails part-way, run the export again. It
  resumes the same invoice and never adds an item twice. If Lago issued an
  invoice but the export stopped before tagging it, the retry names that
  invoice and stops; void it in Lago, then retry.
- **One at a time.** While another request or a billing run is exporting a
  statement, a second export gets `409 statement_invoice_export_in_progress`;
  retry once it finishes.
- **Wrong customer.** If the provider refuses the invoice before creating it,
  for example because the customer doesn't exist, export again with the
  right `--customer` or another provider.
- **Currencies.** Any ISO 4217 currency with up to three decimal places:
  two-decimal ones such as EUR and USD, zero-decimal ones such as JPY and
  KRW, and three-decimal ones such as KWD and BHD. Invoices, payment amounts
  and `amount_minor` fields use the currency's own minor unit (yen for JPY,
  fils for KWD). Stripe writes three-decimal amounts ending in 0 and MGA in
  whole units, so Stripe invoices in those currencies round to that. Paddle
  invoices only USD, EUR and GBP.
- **Payment status.** Gregale checks each exported invoice in the provider
  until it is paid, voided or deleted. It checks drafts every 6 hours and open
  invoices every hour. List what is still owed:

  ```bash
  gregale account invoices --status unpaid
  gregale account invoices --status overdue
  ```

  Each invoice shows whether it was paid, how much remains, and its due date.
  Once a day, Gregale emails you a list of invoices that became overdue,
  reporting each invoice only once.

### Bill a month for every customer

A billing run does the steps above for every customer with billable usage in
one ended UTC month. It covers app consumers and
[platform tenants](#customers-across-several-apps), and runs in the
background:

```bash
gregale account billing-run start --month 2026-09 --export stripe --wait
gregale account billing-run show RUN_ID --outcome held
```

For each customer the run drafts the month's statement and checks
completeness. It then finalizes the statement and, with `--export stripe`,
`paddle` or `lago`, exports it to that provider.

- **When.** A run can start one hour after the month ends, once late usage
  has arrived. Only one run per account is in progress at a time.
- **Outcomes.** Each customer ends up with one of these outcomes:

  | Outcome | Meaning |
  |---|---|
  | `exported` | Invoices were created in the provider. |
  | `finalized` | The statement was finalized and not exported. |
  | `drafted` | The statement was drafted only (`--no-finalize`). |
  | `up_to_date` | Nothing new to bill since the last run. |
  | `held` | Needs your attention, for example unpriced usage or a completeness gap. |
  | `skipped` | Billed through a platform tenant, or already invoiced another way. |
  | `failed` | The provider or Gregale kept failing after retries, or the customer has no ID in the provider. |

  Held and failed customers don't stop the run. Fix the cause, then start
  another run for the same month.
- **Completeness gaps.** A consumer whose usage ledger misses requests that
  telemetry saw is held before finalizing. Use `--no-hold-on-gaps` to
  finalize anyway.
- **Platform tenants.** Usage attributed to a platform tenant is billed on the
  tenant's statement, so those consumers are skipped in the app part of the
  run. Use `--sources app_consumers` to bill every consumer by app statement
  instead.
- **Product Control customers.** With `--sources product_customers` (alone or
  with the other sources), the run also bills each Product Control customer's
  statement for the month, once per app. It is accepted only while Product
  Control is enabled. See
  [Product Control billing runs](ops/product-control.md#bill-every-customer-in-a-billing-run).
- **Running a month again.** Late usage becomes adjustment statements.
  Statements already invoiced stay `up_to_date`. Every finalized statement of
  the month without a handoff is exported, oldest first. Statements with a
  zero total are never exported.

To bill every month automatically, set a schedule. Each month's run starts
`--delay-hours` after the month ends (default 24 hours), beginning with the
current month:

```bash
gregale account billing-run schedule-set --export stripe --delay-hours 6
```

Gregale emails you when a run finishes with held or failed customers, and
after every scheduled run.

### Prepaid credit

Sell prepaid bundles by granting a consumer credit. Finalized statements in
the credit's currency draw it down automatically:

```bash
gregale consumers credit-grant my-api CONSUMER_ID --currency EUR --amount-millicents 5000000 --note "Starter pack"
gregale consumers credits my-api CONSUMER_ID
gregale consumers credit-void my-api CONSUMER_ID CREDIT_ID
```

- **When it applies.** Credit is drawn when a statement is finalized, from
  the grant that expires first, then grants that never expire. It never pays
  more than the statement's amount, and it never crosses currencies.
- **Statements.** Each statement shows `credit_applied_millicents` and
  `amount_due_millicents`, and so does the `usage_statement.finalized`
  webhook.
- **Invoices.** Exported invoices carry a negative "Prepaid credit applied"
  line, so the invoice total is the amount due. Paddle and Lago get one
  summary item instead. Billing runs don't export statements that credit pays
  in full.
- **Expiry and voiding.** Use `--expires` for credit that lapses. Voiding
  removes what is left of a grant; credit already applied stays applied.
- **Scope.** Consumer credit pays app consumer statements. Customers
  across several apps have
  [their own credit](#customer-credit-and-spend-caps).

### Spend caps

Cap what a consumer can run up each month. Once the month's amount due,
after prepaid credit, passes the cap, the gateway refuses the consumer's
requests with `402 consumer_spend_cap_reached`. This lasts until the month
ends, you raise the cap, or you add credit:

```bash
gregale consumers spend-cap-set my-api CONSUMER_ID --currency EUR --monthly-cap-millicents 2000000
gregale consumers spend-cap my-api CONSUMER_ID
```

- **Prepaid only.** A cap of 0 stops the consumer as soon as its credit runs
  out.
- **Soft limit.** Gregale checks each cap every minute, and right away when
  you change the cap or the credit. A consumer can therefore go over by about
  two minutes of traffic. The refused requests are not billed.
- **Repricing.** A check reprices the month only when the consumer made
  requests since the last one, and at least every 15 minutes. Changes other
  than usage, such as a new rate card or credit expiring, can take up to
  15 minutes to reach the cap.
- **Currency.** The cap is compared with the month to date in its own
  currency only.
- **Usage only.** Caps count usage charges, not base fees or minimums.
  Credit pays a statement's fees first, so credit spent on fees does not
  lift a cap.

### Overdue invoices

Stop serving customers who don't pay. Once an exported invoice is unpaid a
number of days after its due date, the gateway refuses that consumer with
`402 consumer_invoice_overdue` until the invoice is paid or voided:

```bash
gregale consumers billing-policy-set my-api --block-overdue-after-days 14
gregale consumers overdue-holds my-api
```

- **What counts.** Exported invoices that are open or uncollectible with an
  amount left. Drafts have no due date and never count.
- **Timing.** Holds are recomputed every 5 minutes. While an invoice holds
  its consumer, Gregale checks its payment every 15 minutes, so paying lifts
  the hold within about 20 minutes.
- **Can't check, won't block.** A hold needs a payment status Gregale read in
  the last 24 hours. If your provider key is rotated or removed, Gregale
  emails you to reconnect it and lets held customers through after a day.
- **Lifting all holds.** `billing-policy-delete` removes the rule; holds lift
  at the next check.

## Let customers see their own billing

Give a customer a key with the `billing` scope, and it can read its own usage,
month-to-date spend and invoices. Use this to build a billing page in your
product, or let customers script it:

```bash
gregale consumers key-create my-api CONSUMER_ID --name billing --scopes read,billing
curl -H "Authorization: Bearer ck_..." https://api.gregale.dev/v1/consumer-billing/my-api
curl -H "Authorization: Bearer ck_..." https://api.gregale.dev/v1/consumer-billing/my-api/usage
curl -H "Authorization: Bearer ck_..." https://api.gregale.dev/v1/consumer-billing/my-api/statements
```

- **Month to date.** The summary prices this month's usage the same way the
  next statement will. It also shows the plan's monthly cap, the prepaid
  credit left, and the spend cap. Late usage can still change it.
- **Statements.** The latest finalized statements show the prepaid credit
  applied (`credit_applied_millicents`), what is left to pay
  (`amount_due_millicents`), and each invoice's payment status and the
  provider's hosted invoice or checkout page, where your customer can pay.
- **Own data only.** A key reads only its own consumer, never other customers
  or your rate cards. A key with only the `billing` scope cannot call your
  app.
- **Limits.** Each key may make 60 requests a minute. A usage request covers
  at most 31 days.

## Let customers sign up themselves

Turn on sign-up, and customers can start using your API without you
creating anything by hand. They pick a plan, save a card with Stripe and get
a consumer key with the scopes you choose:

```bash
gregale account billing-connection connect stripe < stripe-key.txt   # if not connected yet
gregale consumers signup-set my-api --display-name "Weather API" --plans pro,team --trial-days 14
gregale consumers signup my-api     # shows the page URL to link to
gregale consumers signups my-api    # recent sign-ups
```

- **The page.** Link customers to the page URL,
  `https://<gregale domain>/v1/consumer-signup/my-api/page`. It shows the
  plans with their current prices. Gregale hosts the page and runs the
  Checkout in your Stripe account.
- **What a customer gets.**
  - A consumer named after them, whose `external_ref` is their new Stripe
    customer. Exports use that customer by default.
  - The chosen plan and your trial.
  - A key with the scopes from `--key-scopes` (default `write,billing`), so
    they can call your API and [read their own billing](#let-customers-see-their-own-billing).
- **Nothing is charged at sign-up.** Stripe saves the card and Gregale makes
  it the customer's default. Invoices exported with collection `charge`
  charge that card; with `send`, Stripe emails the invoice.
- **Stripe only.** Paddle and Lago cannot save a card without charging, so
  they cannot take sign-ups. A restricted Stripe key needs write access to
  Customers and Checkout Sessions, and read access to SetupIntents.
- **Your own page.** Build your own page on the same API instead:
  1. `GET /v1/consumer-signup/my-api` returns the form.
  2. `POST /v1/consumer-signup/my-api` with `email`, `name` and `plan_id`
     returns a `checkout_url` and a `claim_token`.
  3. After the Checkout, `POST .../signups/{id}/complete` with the claim
     token returns the key.

  Completing again within 24 hours issues a new key and revokes the
  previous one.
- **Limits.**
  - Each address can start 10 sign-ups an hour.
  - An app takes `--daily-limit` sign-ups a UTC day (default 100, at most
    1,000).
  - Sign-ups not completed within 24 hours expire, and Gregale deletes them
    a week later.
  - Failed sign-ups show in your audit log as `api_consumer.signup_failed`
    with the reason, such as a rejected Stripe key.
- **Turning it off.** `gregale consumers signup-delete my-api` closes the
  page. `signup-set --disabled` closes it but keeps the settings.

### Customers manage their own account

With their `billing` key, a customer can change their card, switch plans or
leave without asking you:

```bash
curl -H "Authorization: Bearer ck_..." https://api.gregale.dev/v1/consumer-billing/my-api/plans
curl -X PUT -H "Authorization: Bearer ck_..." -d '{"plan_id":"PLAN_ID"}' https://api.gregale.dev/v1/consumer-billing/my-api/plan
curl -X POST -H "Authorization: Bearer ck_..." https://api.gregale.dev/v1/consumer-billing/my-api/portal
curl -X POST -H "Authorization: Bearer ck_..." https://api.gregale.dev/v1/consumer-billing/my-api/close
```

- **Card and invoices.** `portal` returns a short-lived Stripe customer
  portal link for customers billed through your Stripe. In live mode, save
  your portal settings in the Stripe dashboard once. If Stripe refuses,
  your audit log shows `api_consumer.portal_failed` with the reason.
- **Plans.** `plan` switches between the plans you offer at sign-up, from
  the next minute. Allow it with `signup-set --customer-plan-changes`.
- **Closing.** `close` revokes the customer, so their keys stop working.
  Usage until then is still billed. Allow it with
  `signup-set --customer-closing`.
- Anything you have not allowed gets `409 consumer_self_service_unavailable`.

### Know when customers sign up or change

Subscribe an app webhook to these events to provision accounts, send
welcome mail or update your CRM:

- `consumer.signed_up`: a sign-up created a consumer. It carries the
  customer's name, email, Stripe customer, plan, trial end and whether it
  was test mode.
- `consumer.plan_changed`: a customer switched plans themselves, from and to
  which plan, and from when.
- `consumer.closed`: a customer closed their account themselves.

Changes you make yourself do not send these events.

## Customers across several apps

To bill one customer across several apps, link their consumers to a
[platform tenant](platform-tenants.md). You can then create cross-app
statements and a customer-wide rate card with
`gregale platform-tenants statement-draft` and `rate-card-create`:

```bash
gregale platform-tenants rate-card-create --id TENANT_ID --currency EUR --price-millicents 20 --included-units 50000
gregale platform-tenants rate-card-create --id TENANT_ID --currency EUR --tier 100000:0 --tier inf:15
gregale platform-tenants rate-card-create --id TENANT_ID --currency EUR --price-millicents 20 --weight "POST /generate=5"
gregale platform-tenants statement-draft --id TENANT_ID --month 2026-09
```

- **Shared allowance and tiers.** A tenant card's free requests and tiers
  count the customer's usage across all their apps, in minute order through
  each UTC month.
- **Route weights.** `--weight` charges each of the customer's requests on a
  route that many units, in every app: consumer requests, and requests through
  a tenant surface or JWT rule. Unlisted routes count 1.
- **Compute time and response size.** `--unit compute_second` or
  `--unit response_megabyte` prices the customer's compute time or response
  size across their apps. Each consumer, surface or JWT rule rounds its own
  minute to the nearest second or megabyte. As for app cards, these cards take
  no route weights and can't be backdated. `--tier-mode volume` works here
  too.
- **Units your app counts.** `--unit app:<meter>` prices the units your app
  reports in `Gregale-Usage`, for the customer's consumers and for requests
  through their surfaces and JWT rules.
- **Several units at once.** Customer-wide cards of different units price
  side by side, each with its own free units and tiers. To stop charging a
  unit, add a card for it at price 0.
- **Monthly statements.** While a card with free requests, tiers, route
  weights, monthly fees, or a unit other than requests is in force, cross-app
  statements must cover one calendar month.
  Late usage re-prices the month: the adjustment bills only the difference,
  so a line can be negative when free units move to an earlier minute, but
  the total never is.
- **App cards underneath.** An app's own free requests, tiers, route weights
  or plans don't block a cross-app statement for minutes a tenant card prices.
  A minute only an app card prices still needs a flat app card; otherwise bill
  those consumers with app statements.

### Customer fees, trials and overdue invoices

Customer-wide cards take the monthly fees app cards do, customers can have a
free trial, and you can stop serving customers who don't pay:

```bash
gregale platform-tenants rate-card-create --id TENANT_ID --currency EUR --price-millicents 20 \
  --included-units 1000000 --base-fee-millicents 49900000 --prorate-monthly-fees
gregale platform-tenants trial-set --id TENANT_ID --days 30
gregale platform-tenants billing-policy-set --block-overdue-after-days 14
gregale platform-tenants overdue-holds
```

- **Fees.** A base fee and minimum per customer month, from the card in force
  when the customer's month starts. Statements show them as `fees`, billing
  runs bill customers without usage, and finalized-statement webhooks carry
  them.
- **Trials.** A customer's usage in the trial is priced at zero, and a month
  that starts during it has no fee or minimum.
- **Overdue invoices.** The rule covers all of the account's customers. A
  customer whose exported invoice is unpaid that many days past due gets
  `402 tenant_invoice_overdue` on every request, through consumers, surfaces
  and JWT rules, until the invoice is paid or voided.

### Customer credit and spend caps

Customers across several apps get prepaid credit and spend caps too,
counted over all of their apps:

```bash
gregale platform-tenants credit-grant --id TENANT_ID --currency USD --amount-millicents 5000000 --note "Annual prepay"
gregale platform-tenants credits --id TENANT_ID
gregale platform-tenants credit-void --id TENANT_ID --credit-id CREDIT_ID
gregale platform-tenants spend-cap-set --id TENANT_ID --currency USD --monthly-cap-millicents 20000000
gregale platform-tenants spend-cap --id TENANT_ID
```

- **Credit.** Works like [consumer credit](#prepaid-credit): finalized
  customer statements draw it down, the statement and the
  `platform_tenant.statement.finalized` webhook show
  `credit_applied_millicents` and `amount_due_millicents`, and exported
  invoices carry the credit line. Customer credit pays only customer
  statements.
- **Spend caps.** Once the customer's usage for the month, after credit,
  passes the cap, every request attributed to the customer gets
  `402 tenant_spend_cap_reached`, through consumers, surfaces and JWT rules.
  Caps count usage, not fees, are checked every minute, repriced when the
  customer has new usage or every 15 minutes, and can be overshot by about
  two minutes of traffic, as for consumers.

### Tenant plans

Package a customer-wide price and request budget once, and move customers
onto it:

```bash
gregale platform-tenants plan-create --name growth --currency EUR --price-millicents 20 \
  --weight "POST /generate=5" --max-requests-per-minute 600 --max-requests-per-day 100000
gregale platform-tenants set-plan --id TENANT_ID --plan growth
gregale platform-tenants plan-history --id TENANT_ID
```

- **Assigning.** `set-plan` adds the plan's rate card for the tenant from the
  next minute and sets its [request budget](platform-tenants.md) at once. The
  tenant keeps its earlier cards for earlier minutes. The plan is the
  tenant's whole price: any other unit the tenant is charged for gets a free
  card from the same minute.
- **Fees.** `--base-fee-millicents`, `--minimum-millicents` and
  `--prorate-monthly-fees` set the plan card's monthly fees.
- **Changing a plan.** `plan-update` changes the plan for future assignments.
  Customers already on it keep their price until you run `set-plan` again.
- **Limits.** An account keeps up to 100 tenant plans. A plan's currency must
  match the tenant's existing cards.

## Limits

- Units are requests, optionally weighted by route, the compute seconds or
  response megabytes of billable requests, or units your app reports. App
  and customer-wide cards can price several units at once.
- Usage recorded before an upgrade has no compute time, response size or
  route for surface and JWT-rule requests, so it prices as zero seconds and
  megabytes and weight 1.
- A billable request's usage is written to disk before the client receives
  the end of the response. A gateway crash can therefore no longer lose a
  served request. A request whose usage can't be recorded is not completed
  for the client and isn't billed. The completeness check still compares
  billed requests with request telemetry, to catch other gaps.
- Gregale records the handoff to your billing system, or creates an invoice
  in your own Stripe, Paddle or Lago. Only a connection set to collect has
  your provider send or charge it, and the money never passes through
  Gregale. Paddle can send invoices but not charge them.
- The Paddle and Lago exports follow those providers' published API
  references and were tested against recorded-shape fakes, not live
  accounts.

See [ADR-949](adr/949-app-consumer-statement-revisions-and-platform-failure-billing.md),
[ADR-950](adr/950-api-consumer-monthly-allowances.md),
[ADR-951](adr/951-api-consumer-graduated-tiers.md),
[ADR-952](adr/952-api-consumer-route-weights.md),
[ADR-953](adr/953-api-consumer-plans.md),
[ADR-954](adr/954-api-consumer-usage-completeness.md),
[ADR-849](adr/849-api-consumer-usage-alerts.md),
[ADR-975](adr/975-platform-tenant-allowances-and-tiers.md),
[ADR-1502](adr/1502-stripe-statement-invoice-exports.md),
[ADR-1503](adr/1503-month-end-billing-runs.md),
[ADR-1504](adr/1504-statement-invoice-payment-status.md),
[ADR-1505](adr/1505-usage-durable-before-response-completes.md),
[ADR-1506](adr/1506-consumer-billing-self-service.md),
[ADR-1507](adr/1507-paddle-and-lago-statement-exports.md),
[ADR-1306](adr/1306-consumer-prepaid-credit-and-spend-caps.md),
[ADR-1307](adr/1307-platform-tenant-route-weights-and-plans.md),
[ADR-1308](adr/1308-consumer-compute-and-response-size-pricing.md),
[ADR-1309](adr/1309-volume-rate-card-tiers.md),
[ADR-1312](adr/1312-locked-volume-steps.md),
[ADR-1313](adr/1313-stripe-automatic-tax-on-exports.md),
[ADR-1314](adr/1314-api-consumer-trials.md),
[ADR-1315](adr/1315-block-consumers-with-overdue-invoices.md),
[ADR-1316](adr/1316-monthly-base-fee-and-minimum-spend.md),
[ADR-1317](adr/1317-parallel-unit-price-streams.md),
[ADR-1318](adr/1318-app-reported-usage-units.md),
[ADR-1319](adr/1319-platform-tenant-unit-streams-and-app-units.md),
[ADR-1322](adr/1322-platform-tenant-fees-and-trials.md),
[ADR-1324](adr/1324-block-tenants-with-overdue-invoices.md),
[ADR-1325](adr/1325-prorated-monthly-fees.md),
[ADR-1310](adr/1310-platform-tenant-resource-pricing-and-source-routes.md),
[ADR-1311](adr/1311-statement-invoice-collection.md),
[ADR-20261019100330](adr/20261019100330-statement-currencies.md),
[ADR-20261019100331](adr/20261019100331-platform-tenant-credit-and-spend-caps.md),
[ADR-20261019100332](adr/20261019100332-prorated-fees-across-card-changes.md), and
[ADR-20261019100333](adr/20261019100333-paddle-send-collection.md) for the billing rules.

These decisions were originally numbered ADR-843 through ADR-848. They were
renumbered to ADR-949 through ADR-954 to resolve collisions with route-health
decisions. Already merged migrations retain their original comment citations
so their recorded checksums remain valid.
