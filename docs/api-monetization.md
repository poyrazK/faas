# API monetization

Charge the customers who call your API per request. Gregale identifies each
caller, meters their requests, prices them with versioned rate cards, and
produces auditable usage statements that you invoice through your own billing
system. Gregale does not collect payment from your customers.

API monetization is not released (ADR-20261018090210). It stays off, with
no customer-facing surface, until an operator sets the `api_monetization_enabled`
runtime setting, or releases it to an account with the `api_monetization`
platform flag; `FAAS_API_MONETIZATION_ENABLED=1` is the setting's bootstrap
value for apid and gateways. The CLI shows the selling commands (consumer
plans, rate cards, statements, `mcp stats`, `mcp listing`) to a logged-in
account whose capability view (`GET /v1/capabilities`) does not hide
`api-monetization`; the variable in your shell still shows them everywhere,
including the generated reference. Once released it is
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
`HEAD` and `OPTIONS`, while `write` and `admin` allow every method. To require
a key on every request, run `gregale app my-api --consumer-auth-mode required`.
Browser CORS preflights carry no credentials, so required mode lets a preflight
(`OPTIONS` with `Origin` and `Access-Control-Request-Method`) through to your
CORS rule or app; the request that follows still needs a key.
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
- **MCP servers.** Each `tools/call` is labelled `MCP /tools/<name>` when the
  tool is listed in `gregale-mcp.json`, so `--tool-weight generate=20` prices
  one tool. Protocol requests such as `initialize`, `ping` and list calls are
  free and use no monthly units.

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

### Price one meter by model

Create a flat app-meter card with a base unit price and up to 16 model
overrides. All reported variants share the card's included monthly units.

```bash
gregale api consumer rate-card create my-api --currency EUR --unit app:output_tokens \
  --price-millicents 2 --included-units 1000 --model-price standard=1 --model-price premium=4
```

For `POST /generate`, report the model actually used. For example, a
premium response reports `Gregale-Usage: output_tokens__premium=600`.
Use `output_tokens__standard` for the standard model and plain
`output_tokens` when the base price applies. The meter and model are
lowercase names, each starts with a letter, and the combined name fits
in 32 characters. Once a consumer's price history enables model rates for a
meter, a valid alias without an effective rate appears as unpriced in the
quote and holds statement finalization. An alias with its own effective
standalone rate card is priced on that card. Reported base units and models
with an effective rate consume the shared allowance. Configure overrides
before emitting their aliases and keep them configured while the app still
reports them. A new rate applies
from its effective minute. An earlier pricing gap stays with its original
billing period.

Gregale rechecks the meter ledger before finalizing a priced draft.
Usage recorded after that check belongs in a later statement revision.

Allowance is assigned by UTC minute, with the base meter first and models
in alphabetical order within a minute. Request rate cards can price the
same traffic separately. Model overrides apply to app consumer statements.
Customer-wide tenant statements use their own applicable rates.

### Inspect a consumer's meters

Add `--breakdown` to `gregale api consumer usage` to see raw billable requests
by route and reported units by meter. Add it to `gregale api consumer quote`
to see measured, included and charged units and the amount at each applicable
rate. Requests that lack a route label appear under unattributed requests.
Within a minute, the quote allocates shared request allowances and tiers to
weighted routes first, then other routes, then unattributed requests. Routes
in each group follow alphabetical order. That allocation preserves the
statement's rated totals.

Owner usage and quote endpoints and the customer billing usage endpoint
accept `?breakdown=true`. Quote and finalized statement buckets include the
model on rated model units and the immutable rate card ID.
Where exact request audit is enabled, owners can correlate those totals with
the verified consumer and route in `GET /v1/apps/{slug}/audit/requests`.

### Approve a waiver for unpriced model usage

When a model alias has no effective rate, review the held draft and decide
whether to approve a zero charge for the recorded usage. An administrator can
select one app meter, one alias, and a closed UTC-minute window within that
draft's statement period. Use the exact number of unpriced units in the
selected buckets. For example, save the following as `waiver.json` after
substituting the draft ID, units, and minute boundaries from your statement.

```json
{
  "source_statement_id": "6a5c870c-b450-46d8-ba06-280e49e45dc9",
  "unit": "app:output_tokens",
  "model": "experimental",
  "since": "2026-11-01T14:20:00Z",
  "until": "2026-11-01T14:21:00Z",
  "units": 17,
  "reason": "Approved zero charge for the launch trial"
}
```

```bash
gregale api consumer statement draft my-api "$CONSUMER_ID" \
  --month 2026-11 --waiver-file waiver.json
```

Use the same statement period as the source draft. API clients send the file's
contents as the `waiver` object in `POST /v1/apps/{slug}/consumers/{consumer_id}/usage-statements`.
Gregale records the approver, time, reason, and exact waived units in a new
revision. Repeating the approval returns that revision. If another draft has
superseded the source or the unit count differs, review the latest draft and
submit a new approval. Quotes and statement buckets expose `waived_units`.
Finalize the new revision after all holds are resolved. Additional unpriced
usage, including late units from the same minute, requires a fresh review.

For a first Stripe test-mode invoice, use a test customer and compare the
finalized statement's amount due against Stripe's draft invoice. Include a
paid model and a waived model in the same statement so you can verify that
the waived units are absent from the invoice. Retry export and confirm it
returns the same invoice ID with the same line items and total.

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

### Route limits

Cap one expensive route, or one MCP tool, inside a plan:

```bash
gregale consumers plan-create my-api --name free --max-units-per-month 1000 \
  --route-limit "POST /generate=5/minute,100/month" --tool-limit search=1000/month
gregale consumers plan-update my-api --plan free --route-limit "POST /generate=none"
```

- Limits count requests per consumer, per minute and per UTC month; `0` means
  unlimited and `none` removes a route. Up to 50 routes per plan.
- Over a limit, your consumer gets `429` with `Retry-After` and
  `x-faas-rate-limit-scope: consumer-plan-route-minute` or
  `consumer-plan-route-month`. The response names the route and the limit.
- A request counts toward the route only when the plan admits it too.
- On an MCP server, cap tools with `--tool-limit`, not the endpoint's own
  route: `POST /mcp` then matches only free protocol requests, such as
  `initialize` and list calls. A tool limit applies to any tool name, listed
  in `gregale-mcp.json` or not; statements price a tool by name only when it
  is listed there.

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

### Let consumers buy credit packs

With your app's Stripe connection in place, use an owner key to configure up
to 16 packs. The return URL is your own billing page. This example sells a
USD 20 pack that grants USD 20 in prepaid credit.

```sh
curl -X PUT -H "Authorization: Bearer $OWNER_KEY" -H 'Content-Type: application/json' \
  -d '{"return_url":"https://billing.example.com/credits","packs":[{"id":"starter","label":"Starter credit","currency":"USD","amount_millicents":2000000}]}' \
  https://api.gregale.dev/v1/apps/my-api/consumer-credit-packs
```

In your billing page, use the customer's `billing`-scope consumer key to:

1. List packs with `GET /v1/consumer-billing/my-api/credit-packs`.
2. Start checkout with `POST /v1/consumer-billing/my-api/credit-purchases` and
   a body containing `pack_id` and a client-generated `purchase_id` UUID.
   Save the UUID and reuse it when retrying the same purchase. Send the
   customer to the response's `checkout_url`.
3. Read `GET /v1/consumer-billing/my-api/credit-purchases/{purchase_id}`
   after Stripe returns to your page. Stripe returns the order in the
   `credit_purchase_id` query parameter. Keep showing a pending state until
   `status` is `credited` or `expired`.

Credit packs require a live Stripe connection. Gregale checks Stripe in the
background, including when the customer closes the tab. It adds a single grant
after Stripe confirms the exact live payment.
Show the refreshed prepaid credit balance from the consumer billing summary
once the purchase returns its `credit_grant_id`. The grant applies to
statements in the pack's currency. It has no expiry. If the consumer has a
spend cap in that currency, Gregale schedules a fresh cap check.

Customers can resume one open purchase at a time. Checkout sessions expire
after one hour. To stop new purchases, set `packs` to `[]`; open sessions
keep their saved terms. Gregale blocks changes to the Stripe connection while
purchases are pending. A restricted Stripe key needs Checkout Session read
and write access. Stripe validates charge limits against the merchant's
settlement currency. If Stripe rejects a new checkout, correct the pack as
needed and use a new purchase ID. If you issue a refund in Stripe, review the
grant and void its unused balance in Gregale.

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

### A free plan without a card

Offer a plan whose rate cards charge nothing, and customers can join it
without Stripe Checkout:

```bash
gregale consumers plan-create my-api --name free --max-units-per-month 1000
gregale consumers rate-card-create my-api --plan free --currency USD --price-millicents 0
gregale consumers signup-set my-api --display-name "Weather API" --plans free,pro --free-plan free
```

- **What counts as free.** Every rate card of the plan, in force or
  scheduled, has no unit price, priced tier, base fee or minimum, and the
  plan has at least one. Gregale refuses `--free-plan` otherwise, and
  refuses a charging rate card on the free plan while it is the free plan.
- **No Stripe needed yet.** When the free plan is the only plan you offer
  (`--plans free --free-plan free`), sign-up works before you connect
  Stripe, so you can open an MCP server to signed-in callers first.
  Without a Stripe connection these sign-ups are live, not test: the pages
  show no test-mode notice and the funnel counts them. Offering a paid plan
  asks you to connect Stripe.
- **Joining.** `POST /v1/consumer-signup/my-api` with the free plan's
  `plan_id` returns an empty `checkout_url`; complete the sign-up at once
  with the claim token. The hosted page does this for you. No Stripe
  customer is created, so the consumer's `external_ref` is `signup:<id>`.
- **Moving to a paid plan.** With `--customer-plan-changes`, a customer
  that reaches its plan's limit gets an `upgrade_url` in the `429
  consumer_plan_limit_exceeded` problem. The link opens a hosted page that
  lists your offered plans. A paid plan first saves a card with Stripe
  Checkout; the new Stripe customer becomes the consumer's `external_ref`,
  and the plan changes from the next minute. The link works for 24 hours
  and opens only that customer's plan change. A customer without a card
  cannot switch to a paid plan with its key
  (`409 consumer_self_service_unavailable`).
- **In MCP clients.** An MCP client cannot read a 429 body, so a refused
  MCP request gets an answer the client understands instead (HTTP 200).
  A client that supports URL elicitation shows the person the upgrade page
  (JSON-RPC error `-32042`). Other clients get a tool error, or a JSON-RPC
  error for methods other than `tools/call`, whose text names the limit,
  when a per-minute limit clears and the upgrade link, so the agent can
  tell the person. The answer carries `X-Faas-Error-Code:
  consumer_plan_limit_exceeded` and `Retry-After`.

### MCP callers become customers

For an MCP server behind OAuth (`gregale mcp server auth enable --mode
oauth-proxy`, or `--auth external-oauth`; see
[Host an MCP server](mcp-hosting.md#control-who-can-call-it)), people who
sign in can become your customers on the free plan the first time they call
it:

```bash
gregale consumers signup-set my-mcp --display-name "Search tools" --plans free,pro --free-plan free --oauth-consumers
```

Gregale must check the tokens itself to know who is calling. With
`oauth-proxy` it does. A server made with `--auth external-oauth` checks
tokens in its own code, so also hand that check to Gregale, from the
server's source directory:

```bash
gregale mcp policy --path . --name my-mcp
```

Without it, callers are served but never become customers and are not
metered. `gregale edge rule list my-mcp` shows the `jwt` rule it adds.

- **Who they are.** A customer is the token's issuer and subject: the same
  person signing in again is the same consumer. The token's `email`, when it
  has one, names the consumer; it is never used to match one.
- **Billing and limits.** Their calls are metered, priced and capped by the
  free plan like a consumer key's, including tool route limits.
- **Daily limit.** New customers count against `--daily-limit` with the
  sign-up page's. Past it, new callers get `429 consumer_signup_daily_limit`
  until the next UTC day; existing customers are not affected.
- **Closing someone.** `gregale consumers revoke my-mcp CONSUMER` refuses
  their calls with `403` within five minutes.
- **Turning it on and off.** Changes reach the gateway within five
  minutes. If Gregale cannot reach its API while your server enrolls
  callers, they get `503 consumer_enrollment_unavailable` rather than free
  calls.
- **Customers who already have a key.** Someone who signed up for a key
  and also signs in to your MCP server can make their sign-ins bill to
  that key's account. Their MCP client, or they with the access token it
  holds, send the key once:

  ```bash
  curl -X POST https://my-mcp.gregale.app/.well-known/gregale-consumer-link/mcp \
    -H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
    -d '{"key": "ck_..."}'
  ```

  The path is `/.well-known/gregale-consumer-link` followed by your MCP
  endpoint's path. The answer names the consumer they now bill to; the
  free-plan consumer their first sign-in created is closed. A key that is
  not a live key of your app gets `403 consumer_link_key_rejected`. A
  sign-in already linked to another key gets `409 consumer_link_conflict`:
  `gregale api consumer identity list my-mcp` lists sign-ins and the
  consumers they bill to, and `gregale api consumer identity delete my-mcp
  ID` frees one
  (its next call starts on the free plan again, or it links again).

### Let customers ask about their account from the MCP client

Add `--account-tools` and every signed-in customer sees two more tools in
your server's tool list, answered by Gregale instead of your code:

```bash
gregale consumers signup-set my-mcp --display-name "Search tools" --plans free,pro --free-plan free \
  --oauth-consumers --customer-plan-changes --account-tools
```

- `account_usage`: their plan and its limits (requests per minute, units
  per month, per-tool caps), this UTC month's requests and units, units
  left, and units per tool. It trails live calls by about a minute.
- `account_plans`: the plans you offer with their prices, the one they are
  on and, with `--customer-plan-changes`, an `upgrade_url` to the hosted
  page where a person saves a card. The tool never charges or changes a
  plan.

Calls to these tools are free, do not count against the customer's plan
and work after the plan's limit refuses other calls, so an agent can always
find the way to upgrade. Each customer may call them 30 times a minute on
each gateway node.
While the option is on, the names `account_usage` and `account_plans` are
Gregale's: a tool of yours with either name is hidden from these customers.
Only the first page of a paginated tool list gets them. Customers who use a
consumer key keep the key's [self-service API](#customers-manage-their-own-account).

### Seller stats

See how a sold MCP server is doing, by UTC calendar month:

```bash
gregale mcp server stats --app my-mcp              # this month so far
gregale mcp server stats --app my-mcp --months 6   # the last six months
```

`GET /v1/apps/my-mcp/mcp-stats?months=6` returns the same as JSON, and
agents connected to Gregale's MCP server can call
`gregale_get_mcp_seller_stats`.

- **Per month:** sign-ups by how the customer was created (`oauth` for MCP
  sign-ins, `signup` for the hosted sign-up page, `owner` for customers you
  created; linking a sign-in to an existing customer later does not change
  it), customers with billable units (active; anonymous traffic counts
  toward units only), customers whose month amounts to more than
  zero (paying), closed customers, requests, units and revenue per
  currency.
- **Revenue:** a closed month shows what finalized statements billed,
  before credits; until the month-end run finalizes a customer's month,
  its draft statement counts as an estimate. The current month shows an
  estimate priced exactly as its statement will be. Past 500 customers,
  the estimate covers the 500 with the most units, and pricing stops after
  20 seconds; either way `estimate_complete` is `false`.
- **Tools:** units and distinct customers per MCP tool over the window,
  and this month's estimated revenue per tool. Closed months have no
  per-tool revenue because statements do not split by tool.
- **Near limit:** open customers at 80% or more of their plan's monthly
  units this month, most used first: the customers an upgrade prompt is
  about to reach.

### MCP listing

Give a sold MCP server a public page that people and agents can find and
install from. From the server's source directory (the one with
`gregale-mcp.json`):

```bash
gregale mcp server listing publish --app my-mcp \
  --title "Web search" --summary "Search the web from your agent."
gregale mcp server listing get --app my-mcp
gregale mcp server listing delete --app my-mcp     # unpublish
```

`publish` lists the live server's tools and stores them with the listing.
The page shows the tools as of the last publish, so publish again after
you change them. A republish keeps the title, summary, registry name and
usage setting unless you pass them again. A server that requires a client credential at the
gateway needs `--token-env <ENV>` so the CLI can list its tools.

The page lives at `https://<gregale domain>/v1/consumer-signup/my-mcp/listing/page`
and shows:

- the title, summary and tools;
- the plans you offer on [the sign-up page](#let-customers-sign-up-themselves)
  with their current prices, and a sign-up link while sign-up is open;
- one-click install links for Cursor and VS Code, and a client
  configuration to paste into other MCP clients;
- with `--show-usage`, the number of open customers.

The page serves only while the app takes sign-ups. Its JSON is at
`GET /v1/consumer-signup/my-mcp/listing`, which needs no credential. The
endpoint must be https on the app's own host (its platform URL or default
custom domain); if the app stops answering on that host, for example after
you remove the custom domain, the page stops serving until you publish
again. The owner API is
`GET`, `PUT` and `DELETE /v1/apps/my-mcp/mcp-listing`.

To list the server in the [MCP registry](https://registry.modelcontextprotocol.io),
publish with `--registry-name io.github.<user>/<server>` (the namespace must
be one you can prove to the registry). Then print the registry's
`server.json` and submit it with `mcp-publisher`:

```bash
gregale mcp server listing export --app my-mcp > server.json
mcp-publisher publish
```

The same document is served at
`GET /v1/consumer-signup/my-mcp/server.json`.

### MCP tool contract

Publishing a listing also stores the server's full tool catalog as a
contract. Every production deploy of the app is then checked against it
before it takes traffic, whether you deploy with `gregale mcp server deploy`,
`gregale deploy` or a git push:

- Unchanged, added or widened tools ship.
- Changes that may affect callers, such as a changed description or output
  schema, ship and are listed in the check.
- Breaking changes are refused with `mcp_contract_breaking`, and the
  previous release keeps serving. Removing a tool, adding a required input
  and narrowing an input's type are all breaking.
- A candidate whose tools can't be listed is retried while the deploy
  verifies, then refused.

Tool definitions that MCP clients reject are left out on both sides: publish
warns about them, and a tool that turns invalid counts as removed.

The baseline is the contract of whichever deploy most recently went live,
rollbacks included, so a tool you added last week is protected this week and
a rollback restores the contract it serves. To ship a breaking change on purpose,
say why first. Your customers see the reason only as a dated entry on the
listing page:

```bash
gregale mcp server deploy --name my-mcp --allow-breaking --reason "rename search to web_search"
# or, before a git push or gregale deploy:
gregale mcp server contract allow --app my-mcp --reason "rename search to web_search"
gregale mcp server contract show --app my-mcp
```

`mcp server deploy --allow-breaking` binds the override to the deploy's
own deployments, so no other deploy can use it. `contract allow` admits the
app's next breaking production deploy, whichever path it takes, within an
hour (up to 24 hours with `--expires`). An app can hold at most five open
overrides (one bound to a deploy that ended unused stops counting). An
override also admits a deploy whose tools could not be listed: a bound one
at once, an unbound one only after the check has kept failing for half of
its 5-minute retry window. If `--allow-breaking` cannot record its
override, it cancels that deploy. Creating one is audited, and the check
that uses it records it.
`show` lists the baseline's tools, the recent checks with their changes
(refused ones included), and the open overrides.

A server that checks credentials itself (`external-oauth`) needs a bearer
token for the check to list its tools: store one as an app secret and
publish with `--probe-secret <NAME>`; publish refuses without it. Servers
behind gateway auth need nothing more: the check reaches the candidate
directly.

When a deploy that changed the contract goes live, the app webhook event
`mcp.contract.changed` fires. Its payload has the deployment, the verdict,
both contract digests, whether an override was used, and the changed tool
names. The listing page and its JSON show the last 20 changes, with no
schemas and no reasons. A listing published before this check existed is
not gated until you publish it again. Republishing through the API without
a `contract` keeps the current one and its probe secret; send
`"drop_contract": true` to end the check. Two deploys checked against the
same contract cannot both change it: only the first to go live is
accepted. A production deploy that goes live without a check (a rollback
to one made before the listing) resets the baseline to the listing's
contract. The owner API is
`GET /v1/apps/my-mcp/mcp-contract` and
`POST /v1/apps/my-mcp/mcp-contract/overrides`
([ADR-20261127191645](adr/20261127191645-mcp-tool-contract-gate.md)).

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
`mcp.contract.changed` (see [MCP tool contract](#mcp-tool-contract)) does
fire for your own deploys.

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
  For earlier minutes the statement uses the app's flat request rate in force
  at the time. If those minutes lack a flat request rate, bill them through
  app consumer statements or add a tenant rate that covers them.

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

## Company apps

Billing connections and billing runs belong to your account, so a company
app you created bills its customers through your Stripe connection and your
runs. That lasts while you are a member of the company: once you leave, your
runs stop billing the company app's customers, its sign-up page closes and
its customers' billing portal is unavailable. Customers keep reading their
own usage and statements. See ADR-20261026151103 amendment 2.

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
