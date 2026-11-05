# ADR 20261104211412 · Quota sweep failures page; the Free organization cap holds on transfer and deletion

- **Status:** proposed.
- **Date:** 2026-11-04
- **Amends:** ADR-20261030093512 (plan-time allowance and organization spend
  caps), ADR-202610171904 (organization resource lifecycle)
- **Related:** ADR-20261026120843 (spend-cap holds schedd enforces)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A cost-control audit (2026-11-05) found three gaps that let usage run past the
limits customers and the financial model rely on:

1. **The quota sweep failed silently.** meterd's one-minute quota sweep stops
   Free accounts at their allowance, applies the egress ceiling and records
   the spend-cap holds schedd enforces. When it could not list accounts, load
   spend caps, or read, hold or enforce an account, it logged a warning and
   recorded the tick as successful. `meterd_ops_total{op="quota",code="err"}`
   and the tick health never moved, so a sweep that stopped nobody looked
   healthy.
2. **Transfer multiplied the Free tier.** `FreeOrganizationsOwnedMax` was
   checked only when an organization was created. An account could create a
   Free organization, hand it to another account, and create another, so one
   recipient could own any number of Free organizations.
3. **Delete-then-recreate reset the allowance.** An organization pending
   deletion stopped counting at once, so deleting a Free organization and
   creating a new one granted a fresh month's Free allowance.

## Decision

1. `runQuotaOnce` returns an error when the account list fails, when spend
   caps fail to load (the sweep still proceeds with no caps, fail-open as
   before), or when any account's usage read, spend-cap hold or enforcement
   fails. The rest of the accounts are still swept. The tick records the error
   on `meterd_ops_total{op="quota",code="err"}` and in meterd's tick health.
   A new paging alert, `FaasMeterdQuotaFailing`
   (`increase(meterd_ops_total{op="quota",code="err"}[10m]) >= 3` for 10 m),
   and a runbook section in `docs/runbooks/MeterdBillingPipeline.md` go with
   it.
2. Ownership transfer of a Free, non-personal, live organization fails with
   `OrgFreeLimitError` when the recipient already owns
   `FreeOrganizationsOwnedMax` Free organizations. This landed on main
   separately (#6604, which locks the recipient and the organization first);
   this record adopts it and applies decision 3 to its count. apid answers
   `403 org_free_limit_exceeded`. Paid organizations transfer freely.
3. A Free organization whose deletion was requested in the current UTC month
   still counts toward its owner's cap until the month ends, for creation
   and for transfer. Both `CountOwnedFreeSharedOrgs` and the memory store's
   `ownedFreeSharedOrgsLocked` apply the rule.

## Consequences

- An owner who deletes a Free organization cannot create a replacement until
  the next UTC month (or until they upgrade it). Support can still upgrade or
  restore the organization.
- Persistent Postgres trouble or a single bad account row now pages through
  `FaasMeterdQuotaFailing` instead of passing unseen.
- No schema change: the rule uses the existing `deleted_pending` and
  `deletion_requested_at` columns.
