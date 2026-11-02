# ADR 20261031090658 · The ledger follows the hourly allowance, and Free organizations are bounded

- **Status:** accepted
- **Date:** 2026-10-31
- **Amends:** ADR-20261030093512 (time-weighted allowance), ADR-20261029180000 (Polar organization preflight), ADR-061 (organizations)
- **Related:** ADR-1162 (spend cap), ADR-20261019193600 (ledger dual run), ADR-20261031100000 (the spend cap counts billed overage)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A third billing audit of `main` (2026-10-31) found:

1. **The ledger disagreed with the invoice after a plan change.** The
   provider pusher bills each hour above the allowance in force for that
   hour. ADR-20261030093512 made a downgrade shrink the rest of the month's
   allowance, but `CurrentMonthOverageCents` (spend cap, dashboard, schedd's
   wake gate) and the ledger's compute grant compared the whole month's usage
   with the latest allowance. After a downgrade they counted overage the
   provider never invoices: Scale to Hobby with 2,500 GB-h used by the 15th
   read as $9 against Hobby's $2 cap, parking the apps, while Polar billed
   nothing for it. ADR-20261031100000, decided in parallel, fixed the spend
   cap, dashboard and wake gate (`state.MonthBilledComputeMBSeconds`); the
   ledger's grant still used the month's latest allowance, so the dual run
   reported drift for every account that changed plans.
2. **Organizations multiplied the Free tier.** `POST /v1/orgs` had no limit
   per account, a new organization starts on Free and owns its resources, and
   its apps count against its own Free quota. Each new organization was
   another Free app and another 20 GB-h a month.
3. **A paid plan ending mid-month suspended the account.** The Free hard stop
   counted the whole calendar month, including usage on the paid plan, so an
   account that had used more than 20 GB-h on Pro was suspended the moment it
   returned to Free, although the subscription-ended email says apps run until
   the Free allowance is used.
4. **Billing details never reached Polar.** The Polar provider did not
   implement the billing-information sync, so a business name set through
   `PATCH /v1/account/billing` never appeared on a Polar invoice.
5. **Portal upgrades were charged later.** The Polar organization prorates
   plan changes as `prorate`, which grants a portal upgrade at once and adds
   the difference to the next renewal invoice. A subscription cancelled
   before renewal may never be charged for it.

## Decision

1. **The ledger nets compute the way the pusher bills it.** After a plan
   change in the month, the ledger's compute grant is its own live usage less
   `state.MonthBilledComputeMBSeconds` of it (ADR-20261031100000), with the
   usage before each allowance change read from the ledger's evidence rather
   than `usage_minutes`, so the dual run still compares two sources. A month
   without a plan change keeps the time-weighted grant. The
   ADR-20261030093512 policy is unchanged: a plan change applies from its
   hour on, a downgrade never bills usage the larger allowance covered, and
   an upgrade never takes back overage already billed.
2. **An account owns at most one Free organization.**
   `api.FreeOrganizationsOwnedMax` (1) bounds the shared organizations on the
   Free plan an account owns. `POST /v1/orgs` answers `403
   org_free_limit_exceeded` with the limit and the observed count; upgrading
   or deleting one makes room. Organizations that fall back to Free when a
   subscription ends count too. The check reads before it creates, so a burst
   of concurrent requests can pass it together; the route's MFA requirement
   and rate limit bound that.
3. **The Free quota counts usage since the paid plan ended.** When the
   month's recorded plan history shows a paid plan, meterd's quota loop
   counts usage from the first Free activation after the last paid one. The
   history is read only for Free accounts the whole month would warn or stop,
   once per account and month. A read failure keeps the stricter whole-month
   count. Usage on the paid plan was billed on that plan's terms.
4. **Polar receives the business name.** The Polar provider implements
   `CustomerBillingInfoProvider`: the business name becomes the customer's
   `name`, which Polar prints on invoices, on every billing-information update
   and when the customer is created. Polar requires a structured billing
   address with a country, will not clear one once set, and validates a tax
   ID against that country (sandbox probe, 2026-10-31), so Gregale's
   free-form address and tax ID are not sent; customers enter them at
   checkout or in the customer portal.
5. **Portal plan changes are invoiced when they are made.** The Polar
   preflight requires the organization's `proration_behavior` to be
   `invoice`, beside `allow_multiple_subscriptions=false`. apid's own plan
   changes keep setting their proration per request (`next_period`).

## Consequences

- After a plan change the ledger's dual run agrees with the pusher's
  receipts, as the spend cap and dashboard do since ADR-20261031100000.
- A second Free organization needs the first upgraded or deleted. Paid
  organizations are not limited.
- A cancelled customer gets the Free allowance from the day they return to
  Free; the paid plan's usage no longer suspends them.
- Billing in production needs the Polar organization's proration set to
  "Invoice immediately"; startup refuses the organization otherwise. The
  sandbox organization was switched on 2026-10-31.
- Gregale's billing address and tax ID fields stay local while Polar is the
  provider.
