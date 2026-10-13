# HANDOFF — Stripe statement invoice export (PR #4442)

> **Read this, then delete it.** This file exists only to hand work to the next
> agent. After reading it, remove it in your first commit on this branch
> (`git rm HANDOFF.md`). It must never reach `main`.

## Where things stand

- **Branch:** `api-consumer-stripe-invoices`.
- **PR:** #4442, "feat(billing): export usage statements to the customer's Stripe as draft invoices", open and not merged. The user merges PRs; don't merge it unless they ask.
- **CI:** green on `6a9abd9e8` (23 pass, 1 skipped), including the `cmd/apid` export test.
- **Since then:** `main` moved 158 commits ahead and the PR now **conflicts**. Nothing has been done about that yet. That is the remaining work.

## What the feature is (done and tested)

Finalized usage statements (app consumer and platform tenant) can become draft invoices in the customer's **own** Stripe account. The full design is in `docs/adr/976-stripe-statement-invoice-exports.md`.

- **Connection.**
  - `PUT|GET|DELETE /v1/account/billing-connections/stripe` stores one Stripe key per account.
  - The key is checked against Stripe (`GET /v1/invoices?limit=1`), then sealed with apid's age identity (`mfaRecipient`/`mfaIdentities`, namespace `gregale.billing.stripe.<account>`).
  - Only `key_last4` and live/test mode are ever returned.
  - CLI: `gregale account stripe connect|status|disconnect`; `connect` reads the key from stdin.
- **Export.** `GET|POST …/usage-statements/{statement_id}/stripe-invoice` for app consumers (`/v1/apps/{slug}/consumers/{id}/…`) and platform tenants (`/v1/account/platform-tenants/{id}/…`). CLI: `statement-export` on `consumers` and `platform-tenants`.
- **Invoice shape.**
  - One item per (price source, unit price); free allowance units are no-charge items.
  - Millicents are converted to cents, rounded half away from zero.
  - A rounding item makes the invoice equal the statement total rounded to the cent.
  - Drafts are never finalized or sent (`auto_advance=false`, `pending_invoice_items_behavior=exclude`).
  - Two-decimal currencies only.
- **Resumable.**
  - A `usage_statement_invoice_exports` row is written before any Stripe call, and idempotency keys derive from its ID.
  - Each item carries `metadata[gregale_line]`; a retry lists the invoice's items and creates only missing lines.
  - Completion records the statement's handoff with the Stripe invoice ID.

### Files

| Area | Files |
|---|---|
| Migration | `migrations/20261011081548185_usage_statement_invoice_exports.sql`, `schema.sql`, `pkg/state/project_environment_clone_schema_registry.json`, `pkg/state/sqlc/models.go` |
| State | `pkg/state/statement_invoice_exports.go` (MemStore + PgStore), fields in `pkg/state/memstore.go` |
| Billing | `pkg/billing/statement_invoice.go` (+ `_test.go`): statement → invoice lines and rounding |
| Stripe client | `pkg/billing/stripeinvoice/client.go` (+ `_test.go`): small REST client with a configurable `BaseURL`, independent of Gregale's own `pkg/billing/stripe` |
| API | `pkg/api/statement_invoices.go`: DTOs, client methods, `CodeBillingProviderUnavailable` |
| apid | `cmd/apid/handlers_statement_invoices.go`, `cmd/apid/handlers_statement_invoice_sources.go`, `cmd/apid/handlers_statement_invoices_test.go` (fake Stripe), `stripeInvoices` field and routes in `server.go` |
| CLI | `cmd/gregale/commands_account_stripe.go`, `statement-export` in `commands_consumers.go` and `commands_platform_tenant_billing.go`, `cli_meta.go`, `commands4.go` (`account stripe`), `docs/cli-reference.md` |
| Spec and SDKs | `api/openapi.yaml` = `pkg/apid/openapi.yaml`, `sdk/node/src/generated/**`, `sdk/python/faas_sdk/**`, `cmd/sdk-coverage/main.go`, `scripts/ci/speccompliance/spec_compliance_test.go` |
| Docs | `docs/adr/976-stripe-statement-invoice-exports.md`, `docs/adr/README.md` row, "Send statements to Stripe" in `docs/api-monetization.md` |

## Remaining work: bring the PR up to date with `main`

1. **Merge.** Run `git fetch origin main && git merge origin/main`. Conflicts: `api/openapi.yaml`, `pkg/apid/openapi.yaml`, `docs/adr/README.md`, `pkg/apid/apidserver/server.go` and `sdk/python/faas_sdk/models/__init__.py`.
2. **apid moved.** `main` moved `cmd/apid` into `pkg/apid/apidserver` (`package apidserver`, #4481, ADR-977).
   - `git mv` the three `cmd/apid/handlers_statement_invoice*.go` files into `pkg/apid/apidserver/` and change `package main` to `package apidserver`.
   - In `pkg/apid/apidserver/server.go`, keep `main`'s version and re-add:
     - the `stripeInvoices *stripeinvoice.Client` field;
     - the import of `github.com/onebox-faas/faas/pkg/billing/stripeinvoice`;
     - the 7 routes: 2 next to each statement `…/handoff` route, plus `GET|PUT|DELETE /v1/account/billing-connections/stripe` with `ScopesSecretsWriteSurface` for PUT and DELETE.
   - `mfaRecipient`, `mfaIdentities`, `decodeJSON` and `writeJSON` already exist in `apidserver`.
3. **ADR number.** `main` now has `docs/adr/976-route-advisor.md`, so this ADR needs a new number.
   - `main`'s highest number is about 1154, and open PRs claim numbers too: `gh pr list --state open --limit 80 --json title -q '.[].title' | grep -oE 'ADR-?[0-9]+'`.
   - Rename the file and its README row, then rewrite every `ADR-976` / `adr: 976` that belongs to this feature: `git grep -n "ADR-976\|adr: 976"`. Leave the route-advisor ones alone.
   - The new migration's header comment may be edited, because it isn't merged yet.
   - Run `bash scripts/ci/check_adr_number_uniqueness.sh`. Duplicates that already exist on `main` aren't yours to fix.
4. **Migration version.** `main` has migrations newer than `20261011081548185`. If `migrations/version_test.go` or CI requires a newer version, run `go run ./cmd/migration-new -name usage_statement_invoice_exports`, move the SQL into it, delete the old file, and keep the `-- filename:` header in sync.
5. **Regenerate; don't hand-edit generated files.**
   - Specs: resolve `pkg/apid/openapi.yaml` by keeping both sides, then `cp pkg/apid/openapi.yaml api/openapi.yaml`. Lint with `vacuum lint -r api/vacuum.yaml api/openapi.yaml`; it must pass, and `description-duplication` is an error.
   - `sqlc generate`.
   - Python SDK: `/opt/homebrew/opt/python@3.14/bin/python3.14 sdk/python/scripts/gen.py`. This uses openapi-python-client 0.29.0, the pinned version, and is slow.
   - Node SDK: `npm --prefix sdk/node ci --ignore-scripts && npm --prefix sdk/node run gen`.
   - CLI reference: `go run ./cmd/gregale man --markdown > /tmp/x.md && cp /tmp/x.md docs/cli-reference.md`. Never hand-edit it; `TestMarkdownReferenceFresh` checks it byte for byte.
6. **Test, one package at a time.** Memory is the bottleneck on this machine, so never run builds in parallel.
   - Use `GOFLAGS=-p=1 GOMAXPROCS=2 GOGC=25`. For `cmd/apid` / `apidserver`, add `-gcflags=github.com/onebox-faas/faas/<pkg path>=-dwarf=false -c=1`, as CI does.
   - Postgres-backed `pkg/state` tests can't run locally (no Postgres); CI runs them.
   - If the compiler is killed for memory, push and let CI run it.
7. **Push and watch.** Push to `api-consumer-stripe-invoices`, then watch PR #4442 with single `gh pr checks 4442` calls rather than a background watcher. Fix until green, report, and don't merge.

## User conventions (from memory)

- **No subagents.** Do the work directly.
- **No auto PRs.** Push branches; open PRs only when asked. PR #4442 already exists.
- **One PR per fix.** Keep this scope in #4442; don't split it.
- **Never push to `main`.** Everything lands through a PR.

## Next ideas the user was offered (not started)

1. Prepaid credits and minimum commitments.
2. A read-only portal API for consumers (`/v1/me/...`).
3. Route weights and plans on platform-tenant rate cards (ADR-975 follow-up).
4. Alerts for consumers without a monthly cap.
5. Volume (all-units) tiers.
6. Durable usage write before the response (latency trade-off).
