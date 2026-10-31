# ADR 20261030093512 · The allowance follows time on each plan, and organizations have their own spend cap

- **Status:** accepted
- **Date:** 2026-10-30
- **Amends:** ADR-1162 (a month's allowance follows its plan history), ADR-20261029180000 (default spend cap), ADR-20261026120843 (spend cap holds)
- **Related:** ADR-20261029094512 (subscription reconciliation)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A second billing audit of `main` (2026-10-30) found:

1. **A brief upgrade buys a month's allowance.** ADR-1162 gave a month the
   largest allowance any of its plans granted, so a mid-month downgrade never
   shrinks it. Polar's customer portal lets a customer switch plans themselves
   with prorated charges (the sandbox organization has
   `customer_portal_settings.subscription.update_plan` on). A few minutes on
   Scale cost cents and raised the month's included usage from 200 to
   3,000 GB-h. Portal plan changes also skip apid's downgrade check.
2. **Organizations had no spend cap.** An organization that owns its
   resources is its own billing subject, but caps lived only on `accounts`.
   meterd loaded no organization cap, the default cap skipped organizations,
   there was no route to set one, `account_spend_cap_holds` could not name an
   organization, and schedd's wake gate checked the cap of the member who
   created the app.
3. **The cap stopped apps silently.** When a hold engaged, meterd recorded it
   and counted a metric. With the default cap (Hobby $2), customers would see
   apps stop with no notice.
4. **Downgrades keep apps above the new plan's limits.** The downgrade check
   runs only when it is requested.
5. **The allowance is per calendar month and the fee per subscription
   period.** Subscribing on the 30th granted that month's whole allowance and
   the next month's within one paid period.
6. **A new subscription ends dunning without the failed order being paid.**

## Decision

1. **Each plan's allowance counts for its share of the month.**
   `state.MonthComputeIncludedMBSeconds` weights each recorded plan activation
   by the time it held, and takes the plan active at the hour to hold until
   the month ends. One plan all month grants its whole allowance; five
   minutes on Scale grant five minutes' share. This also closes item 5, and a
   portal plan switch cannot be used to inflate the allowance. A plan change
   is still never retroactive: an activation applies from its recorded minute.
   The pusher, the spend cap (`monthOverageCents`, which counts an uncaptured
   plan change from now), invoice range math and the financial ledger's
   compute grant (`time_weighted_period_grant_quantity_share_v1`) share it, so
   the ledger dual run stays in agreement. A downgrade now shrinks the rest of
   the month's allowance in proportion. Usage the provider already billed is
   never re-billed, and usage the larger allowance covered is never back-billed.
2. **Organizations have their own spend cap.** `orgs.overage_cap_cents`
   mirrors the account column (migration `20261030101542731`, expand). The
   store's cap reads, writes and meterd's bulk load cover organizations. The
   default cap follows an organization's plan exactly as an account's does.
   `account_spend_cap_holds` is keyed by `resource_namespaces`. Owners set the
   cap with `POST /v1/orgs/{slug}/overage-cap` (admin scope, MFA, step-up) and
   members read it with `GET`. schedd's wake gate and frozen thaw check the
   cap of the subject meterd bills: the organization when it owns its
   resources, else the account. The answer is cached per organization for a
   minute. Job dispatch keeps the account, because job usage is billed to it.
3. **The customer is told when the cap stops their apps.** meterd emails
   once per account, month and cap when a hold engages (provider idempotency
   key `<id>:spend_cap_reached:<month>:<cap>`), with the cap, the overage and
   how to raise it.
4. **Not changed: limits after a downgrade.** The subscription-ended email
   already promises that apps above a lower plan's limits keep running (within
   the allowance, then the cap) and that new deploys must fit. A portal
   downgrade lands in the same state. Enforcing deployed-app and RAM limits
   afterwards would be a product change.
5. **Not changed: the failed order after a revoke.** Polar offers no
   off-session charge (`off_session_charges_enabled` is off), so Gregale
   cannot collect an order Polar gave up on. The default cap bounds what such
   an order can hold.

## Consequences

- Billing in a month with a plan change follows the plans' time shares, so
  invoices, the dashboard, the cap and the ledger agree.
- An organization upgraded to a paid plan gets that plan's default cap, and
  its apps stop at the organization's cap, not a member's.
- meterd may email a customer once per month and cap; a second meterd replica
  that sees the same transition dedupes at the provider.
- Rejected: turning off Polar's portal plan switching. apid sends existing
  subscribers to the portal to upgrade, so that needs an API upgrade path
  first, and the time-weighted allowance removes the exploit either way.
