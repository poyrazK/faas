# ADR 20261031211500 · Budgets follow the subject that pays

- **Status:** accepted
- **Date:** 2026-10-31
- **Amends:** ADR-566 (budgets are owned by an account), ADR-20261030093512
  (organization spend caps), ADR-20261028083324 (budget admission outage grace)
- **Related:** ADR-1162 (spend cap), ADR-1430 (USD), ADR-1746/1750 (strict
  compute)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A cost-control audit of `main` (2026-10-31, fb14b672e3) found that budgets
and the spend cap still assume every app is paid for by the account that
created it. An organization that owns its resources is its own billing
subject (ADR-20261030093512): meterd records its apps' usage, ledger evidence
and price contracts under the organization, and Polar bills it.

1. **The organization's spend cap did not stop running work.** The wake gate
   and frozen thaw read the organization's cap, but the reaper's park check
   (`appMustPark`) and the warm pool read the cap of the member who created
   the app (`AppResourceAccount` keeps that member's ID as the storage
   namespace). An organization over its cap kept its running, frozen and
   warm-pool instances; a member's personal cap parked the organization's
   apps.
2. **Organizations could not have budgets.** Budget routes, the scope check,
   the meterd monitor (`ListAllAccounts`) and preview stops were keyed by the
   human account. A member could create an app budget for an organization's
   app, but that app's evidence is recorded under the organization, so the
   budget read $0 and never alerted: missing coverage shown as zero cost,
   which ADR-566 forbids.
3. **Organization evidence had no environment.** The evidence trigger matched
   environments by `environment.account_id = account.id`. For an
   organization's evidence the account is the organization and the
   environment's account is the member who created it, so `environment_id`
   was always missing and environment budgets read nothing.
4. **A pre-USD draft blocked every app of its account.** Budget specs are
   validated on read and must be USD. The USD migration refused only enabled
   EUR budgets, so a disabled EUR draft could remain; every policy read of its
   account then failed, including gateway and wake admission.

## Decision

1. **The payer.** A budget, its evidence and its stops belong to the subject
   that pays for the workload: the organization when it owns its resources
   (`orgs.resource_ownership_enabled AND NOT personal_org`), else the account.
   `state.FinancialBudgetPayer` and the `FinancialBudgetScopeOwned` query
   implement the same rule. A member's budget no longer covers apps an
   organization pays for, and its scopes cannot name them.
2. **The reaper and warm pool use the billed subject's cap**
   (`spendCapSubject`), as the wake gate and thaw already did.
3. **Organization budgets.** `GET/POST /v1/orgs/{slug}/billing/budgets`,
   `GET/PUT/DELETE /v1/orgs/{slug}/billing/budgets/{id}`,
   `GET .../{id}/revisions`, `POST .../budgets/preview` and
   `GET /v1/orgs/{slug}/billing/costs` serve the organization's billing
   subject with the account handlers (`orgbilling.Scope`). Members read;
   the owner or billing role writes, with an admin-scoped credential, MFA and
   an Idempotency-Key. The CLI takes `--org SLUG` on `billing costs`,
   `billing budget-preview` and `billing budgets`.
   - An organization that does not own its resources is refused (422): its
     usage is billed to the members who created it.
   - Monitored `notify` and compute-only `stop_previews` policies activate.
     Alerts go to the organization's billing contact. Notification channels
     stay with account budgets.
   - Strict budgets are refused for organizations (422
     `financial_budget_activation_unavailable`). Strict funding is keyed to
     accounts in the native owner, the ledger triggers and the books
     (ADR-1746), and organization strict funding needs its own native
     acceptance. Until then a member's strict account budget keeps funding
     the apps that member created, including organization apps: that can stop
     them early but never funds more than the member's limit.
4. **meterd** evaluates the subjects that have an enabled monitored budget,
   read in one query (`FinancialBudgetMonitoredSubjects`), instead of reading
   every account's policies on every pass.
5. **Preview stops follow the payer.** The gateway, schedd and the preview
   command guard (`financial_preview_command_held`) read the payer's stops. An
   environment is matched by its project, not by the account that created it;
   the policy's environment was checked against the payer when it was written.
6. **Evidence attribution.** Migration `20261031210054142` matches an
   environment by project and slug, and accepts the organization's own apps.
   Recorded evidence is immutable and keeps its attribution: organization
   environment budgets cover usage from the migration on.
7. **Legacy drafts.** A stored, disabled EUR draft decodes. Every write still
   requires USD, so it can be updated to USD or deleted but never enabled in
   EUR; the API marks it `legacy_currency_draft`.

## Not changed

- **Strict commitments include paused and frozen time.** ADR-1750 funds
  warm and frozen instances like running ones, so a strict budget can hold
  before the invoice reaches its limit. That is the safe direction. Strict
  responses now carry `commitments_include_paused_time`, and the docs say so.
- **The gateway does not check the spend cap for warm traffic.**
  ADR-20261028083324 said it did. schedd's reaper parks a capped subject's
  live instances on its sweep and the wake gate refuses new wakes; this
  record corrects that sentence.

## Consequences

- An organization's apps stop at the organization's cap, including running,
  frozen and warm-pool instances.
- Members lose the ability to budget apps their organization pays for. Such
  budgets read $0 before; a new one is refused with 404 (scope not owned).
  Existing ones keep their revision but no longer match those apps.
- Organization alerts depend on a billing contact; one without it fails
  delivery and retries, like an account without email.
- Rollback: the migration's Down restores both functions. Organization
  budgets written before a rollback remain rows the older binary never
  evaluates.
