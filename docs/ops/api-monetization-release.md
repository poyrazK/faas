# Releasing API monetization

API monetization covers rate cards, plans, statements, credits, invoice exports, billing runs and their gateway enforcement (`docs/api-monetization.md`). It ships switched off. There are two ways to turn it on (ADR-20261020170649):

| Control | Releases to | Use it for |
| --- | --- | --- |
| `api_monetization` platform flag | the accounts its rules admit | trying it on internal or design-partner accounts |
| `api_monetization_enabled` runtime switch | every account | general availability |

An account has monetization when the switch is on or the flag admits it. The flag is decided by account only, so plan rules never match.

## Qualify against the providers first

`make monetization-sandbox` runs the export path end to end against the real provider sandboxes:
- draft invoices with a credit line;
- a retry after the provider refused the customer;
- concurrent exports of one statement;
- send and charge, with payment status synced back;
- JPY and KWD invoices in Stripe's units;
- Paddle send waiting for a customer address;
- a Lago invoice.

Put the keys in `~/.config/gregale/monetization-sandbox.env` (mode 0600), or in the environment. `GREGALE_SANDBOX_ENV_FILE` points the test at another file.

```
GREGALE_SANDBOX_STRIPE_KEY=sk_test_…        # test mode only
GREGALE_SANDBOX_PADDLE_KEY=pdl_sdbx_apikey_… # sandbox only
GREGALE_SANDBOX_LAGO_KEY=…                   # optional: a Lago organization kept for testing
GREGALE_SANDBOX_LAGO_URL=https://api.getlago.com
```

- **Skipped providers.** A provider without keys is skipped.
- **Live keys.** The test refuses live Stripe and Paddle keys.
- **Lago.** Lago has no test mode, so point it at a test organization.
- **Cleanup.** The test creates its own customers and voids, cancels or deletes the invoices it made.
- **Where it runs.** It needs outbound HTTPS from the machine running it, no database.

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

## Turning it off

- **For one account.** `gregalectl flags revoke --key api_monetization --account-id <uuid>`.
- **For everyone admitted by the flag.** `gregalectl flags disable --key api_monetization`.

Nothing is deleted. Billing runs and invoice syncs of accounts it no longer covers are set aside, and are picked up again when it does.

## General availability

Set `api_monetization_enabled` to `true` in the operator console, or turn the flag's default on with no rules. Then publish `docs/api-monetization.md` and list the capability.
