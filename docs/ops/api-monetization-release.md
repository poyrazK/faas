# Releasing API monetization

API monetization covers rate cards, plans, statements, credits, invoice exports, billing runs and their gateway enforcement (`docs/api-monetization.md`). It ships switched off. There are two ways to turn it on (ADR-20261020170649):

| Control | Releases to | Use it for |
| --- | --- | --- |
| `api_monetization` platform flag | the accounts its rules admit | trying it on internal or design-partner accounts |
| `api_monetization_enabled` runtime switch | every account | general availability |

An account has monetization when the switch is on or the flag admits it. The flag is decided by account only, so plan rules never match.

## Qualify against the providers first

`make monetization-sandbox` runs the export path end to end against the real provider sandboxes:
- model pricing, waivers, and late usage against a Stripe draft;
- draft invoices with a credit line;
- a retry after the provider refused the customer;
- concurrent exports of one statement;
- send and charge, with payment status synced back;
- JPY and KWD invoices in Stripe's units;
- Paddle send waiting for a customer address;
- a Lago invoice.

Put the keys in `~/.config/gregale/monetization-sandbox.env` (mode 0600), or in the environment. `GREGALE_SANDBOX_ENV_FILE` points the test at another file.

```
GREGALE_SANDBOX_STRIPE_KEY=sk_test_…
GREGALE_SANDBOX_PADDLE_KEY=pdl_sdbx_apikey_…
GREGALE_SANDBOX_LAGO_KEY=…
GREGALE_SANDBOX_LAGO_URL=https://api.getlago.com
```

- **Skipped providers.** A provider without keys is skipped.
- **Live keys.** The test refuses live Stripe and Paddle keys.
- **Lago.** Lago has no test mode, so point it at a test organization.
- **Cleanup.** The suite creates its own customers. It deletes, cancels or voids unpaid fixtures where the provider allows it. Paid Stripe invoices remain in test mode.
- **Where it runs.** It needs outbound HTTPS from the machine running it, no database.

### Run the model metering pilot

```sh
make monetization-sandbox RUN=TestMonetizationSandboxStripeMetering
```

The test runs apid locally and creates a dedicated Stripe sandbox customer. It
records usage for two priced models and one unknown model, confirms the hold,
approves a waiver for the unknown usage, and exports a USD 3.00 draft. It
checks that a retry returns the same invoice ID and that the draft still has
automatic collection off and exactly the three expected lines. A late priced
event adds only USD 0.10 to a new revision. Late unpriced usage holds the next
revision. Stripe fixtures are cleaned up after the run. `SKIP` means the
Stripe sandbox key is required to complete the pilot.

Before enabling a design partner, repeat the flow on a test deployment that
includes the change. Enable the account flag for the test account, send traffic
through its gateway, and compare the statement's amount due and invoice lines
with the Stripe sandbox draft.

### Verify two reviewed Stripe billing cycles

Run the focused check to qualify Stripe Review & send for the same customer across two closed billing months. Release qualification fails when the Stripe test key is missing.

```sh
GREGALE_REQUIRE_SANDBOX_STRIPE=1 make monetization-sandbox RUN='^TestMonetizationSandboxStripeReviewedInvoice'
```

The check uses the monthly billing runs and replays draft export and send approvals. Stripe must report exactly two invoices for the test customer. Gregale must sync both positive payments and keep them out of First Paid and Repeat Payment in the live funnel.

To run the same two-month workflow against the local Stripe fixture, use the focused test below. Stripe test mode skips email delivery, so verify actual receipt during the live pilot. [Stripe send invoice API](https://docs.stripe.com/api/invoices/send?api-version=2024-06-20)

```sh
make monetization-sandbox RUN='^TestMonetizationReviewedStripeBillingMonthsLocally'
```

The console exposes batch send from a completed draft run when `VITE_FEATURE_BILLING_RUN_DRAFT_EXPORT` and `VITE_FEATURE_API_INVOICE_SEND` are enabled. Test-mode batches use those two flags. Set `VITE_FEATURE_BILLING_RUN_LIVE_SEND=1` after the Stripe check passes to allow live batches in the console. An owner reviews and approves the selected invoices on the current page. Scheduled runs continue to create draft statements for review.

Enable `VITE_FEATURE_BILLING_RUN_COLLECTIONS=1` with draft export review to show the recorded Stripe balances across a completed draft run. The totals group currency and Stripe mode. The on-time payment rate uses live invoices with a positive recorded balance or payment whose due date has passed. **Check Stripe now** reads the selected invoice and updates its last check time after Stripe confirms the balance. On a provider error, Gregale keeps the previous balance and check time.

If a send response was lost and the invoice has since received payment, Gregale can recover the send receipt from Stripe's invoice events. Grant the connected restricted key Events read access for this recovery. Stripe retains events for up to 30 days. [Stripe Events API](https://docs.stripe.com/api/events/list)

For a usage hold created before the run captured hourly gap counts, reconcile usage and start a new draft run for that month. The original hold lacks the recorded counts needed for recheck.

## Release to selected accounts

```
gregalectl flags enable --key api_monetization --reason "monetization preview" --yes
gregalectl flags allow  --key api_monetization --account-id <uuid> --reason "internal account" --yes
gregalectl flags inspect --key api_monetization --account-id <uuid>
```

Within seconds the flag reaches apid and every `gatewayd-internal`. `gregalectl flags show --key api_monetization` lists each daemon's apply receipt.

For accounts the flag admits:
- **Routes.** Its routes, webhook events and the consumer key `billing` scope work.
- **Gateway.** Gateways enforce plans, spend caps and overdue holds, hold responses until usage is durable, and consume `Gregale-Usage`.
- **Workers.** Billing runs, invoice syncs and spend cap checks run.

Other accounts get the unregistered-route `404` right after authentication. Unauthenticated requests reach authentication (`401`) once any account has the flag. The served OpenAPI document and docs omit monetization until every account is included.

Customers of an account use the CLI verbs with `FAAS_API_MONETIZATION_ENABLED=1` in their shell.

## Measure the Stripe pilot

After the two-month Stripe sandbox check passes, admit one to three approved API owner accounts whose apps have recent billable customer registrations. Enable `VITE_FEATURE_API_MONETIZATION_FUNNEL=1` in the pilot console build and open each app's Consumers tab. Record the app's starting counts and revisit them after the next billing cycle. The pilot succeeds when the same customer pays a positive live Stripe invoice in their first paid billing month and in their next billable month.

The funnel evaluates the latest 50 registrations within 120 days and shows the cohort size when more customers registered. Completed test-mode signups and platform-managed consumers are excluded. Usage activation uses the same first-30-day pricing evaluation as first-invoice progress. A send uses Gregale's recorded Stripe send receipt, and a payment uses a recorded positive live Stripe payment. These milestones count independently so automatic collections also appear. Repeat payment uses the next UTC billing month with a finalized positive statement balance and no overlap with the first paid statement. Multiple payments on revisions within one month count once.

Open **Review next invoices** in the app funnel to see the next statement for customers who have already paid a live Stripe invoice. The review also surfaces draft statements and usage holds while they are being resolved. The existing review and send controls apply to live Stripe drafts. Open invoice details to copy a payment link, or use the Personal billing workspace to recheck a held run.

## Turning it off

- **For one account.** `gregalectl flags revoke --key api_monetization --account-id <uuid>`.
- **For everyone admitted by the flag.** `gregalectl flags disable --key api_monetization`.

Nothing is deleted. Billing runs and invoice syncs of accounts it no longer covers are set aside, and are picked up again when it does.

## General availability

Set `api_monetization_enabled` to `true` in the operator console, or turn the flag's default on with no rules. Then publish `docs/api-monetization.md` and list the capability.
