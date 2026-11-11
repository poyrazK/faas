# ADR 20261107160000 · Monitored budgets are GA; strict compute follows the billing subject

- **Status:** accepted
- **Date:** 2026-11-07
- **Amends:** ADR-1750 (strict app compute reservations, its amendments 1
  and 2), ADR-1748 (account strict egress), ADR-20261031211500 (budgets follow
  the subject that pays)
- **Related:** ADR-149 (prepared network cache), ADR-568 (native process
  recovery), ADR-1700 (platform flags)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Budgets are listed as `beta` (catalog id `budgets`, GA inventory row 152).
Launch needs them GA. Two things stood in the way.

1. **Strict budgets cannot run in production yet.** A strict
   (`suspend_workloads`) budget activates only when every active compute node
   has a current financial compute owner (ADR-1750). vmmd registers one only
   under `native_process_recovery` (`cmd/vmmd/financial_compute.go`), which
   production does not run. Under native recovery the prepared network cache
   is refused (ADR-149), and on the n2 test node a wake paid about 0.5 s of
   `setup_network` and 0.6 s of native helper work. That misses the wake
   target. The `strict_budgets` platform flag (ADR-1750) is off for every
   account.
2. **Strict compute followed the app's creator, not its payer.** An
   organization that owns its resources pays for its apps
   (ADR-20261031211500), but strict policies, books, funding and activation
   were read by `apps.account_id`, the member who created the app. A
   member's strict account budget therefore:
   - funded and stopped the organization's apps;
   - waived their capacity floors, so their floor minutes were never billed;
   - counted their unbudgeted attempts as unfunded account compute, which
     refused the member's own strict attempts while a company app ran;
   - could not activate while a company app was running.

   Organizations cannot hold strict budgets (ADR-20261031211500), so nothing
   was meant to cap the company's apps at all.

## Decision

### Monitored budgets are GA; strict budgets stay beta

The `budgets` capability is GA. It covers monitored budgets for accounts and
organizations: policies and revision history, threshold and
projected-overrun alerts, notification channels, the object storage meter,
budget previews, and compute-only `stop_previews`.

Strict budgets move to their own capability, `strict-budgets`, which stays
`beta` behind the `strict_budgets` flag. That covers strict app, job,
project, environment and account compute, and the account egress cap.

Strict budgets become GA through a separate track. It starts with a
design-first ADR-149 amendment: journaled prepared-network claims under
native recovery. Then come the n2 vmmd-restart rerun and the native
acceptance runbook, a production rollout of `native_process_recovery` on
every compute node, and finally releasing the flag.

### Strict compute follows the billing subject

Every strict compute decision about an app is made for the subject that pays
for it. An app that an organization owning its resources pays for (a
non-personal org with `resource_ownership_enabled`) is outside every book
and policy of the member who created it. Organizations still hold no strict
budgets, so such an app runs unbudgeted:

- `FinancialComputePolicies` and attempt funding return no policies for it.
  Its attempts are registered like any unbudgeted boot, are never
  `account_funded`, and vmmd's bypass guard, the scheduler and the dispatch
  check treat it as uncapped.
- `FinancialComputeAppManaged` and `FinancialComputeFloorCovered` are false.
  The meter bills its residency and floor minutes like any unmanaged app,
  under the organization.
- The account book's live attempts and the account activation check leave out
  its attempts, unless the account book funded them, and leave out its
  instances, tasks and billing intervals. A member can activate an account cap
  while company apps run, and their unfunded attempts never make the member's
  book "mixed".

The predicate is the one budget scope validation already uses
(`resource_ownership_enabled AND NOT personal_org`). Jobs have no
organization and are unchanged.

An attempt the creator's book had already funded keeps that funding until it
settles, just as when a budget is disabled. The app's next attempt is
unbudgeted. This only happens when an organization adopts its resources while
an attempt is in flight.

Leases still record the creator in `account_id`: it is the storage namespace
vmmd and the scheduler pass, and changing it would need a vmmd wire field.
Organization strict budgets (the native track) must therefore key books and
leases by the billing subject before they are allowed.

## Consequences

- A member's strict budget can no longer stop, fund or under-bill an
  organization's apps, and company apps no longer block or break the member's
  cap. Covered by `TestFinancialComputeFollowsBillingSubject` (MemStore and
  PostgreSQL).
- The catalog, the capability page and the GA inventory list monitored
  budgets as GA and strict budgets as beta. `TestBudgets` in
  `tests/journeys` covers the GA surface end to end.
- No migration: the change is in queries and store logic only.

## Rejected

- **Promote strict budgets now.** No production node can activate one, and
  turning on native recovery fleet-wide would trade the wake target for it.
- **Hold all budgets in beta until strict is ready.** Monitored budgets do
  not depend on the native runtime and are what most customers need at
  launch.
- **Put the organization in the lease's `account_id`.** That needs a new vmmd
  launch field, which must be released at N-1 first. It belongs with
  organization strict budgets.
