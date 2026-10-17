# ADR-1430: USD pricing and plan entitlements from the October 2026 COGS report

Status: accepted.

Amends spec §4.7 (prices, included quotas and the overage meter), the
CLAUDE.md hard limits (financial model §1), ADR-1320 (object-storage plan
values) and the managed PostgreSQL plan entitlements.

## Context

The October 2026 COGS report (internal, prepared 11 October 2026) costs a
proposed plan table on OVH RISE-S bare metal, Cloudflare R2 object storage and
Neon PostgreSQL. At 500 paying customers it finds about 67% gross margin
(57–62% after the costs outside base COGS), with compute at about €0.003 per
GB-RAM-hour against the €0.01 overage price. Database and storage, not
compute, dominate COGS.

The report keeps the prices (€0 / €9 / €29 / €99) and changes what each plan
includes: more RAM per app, more included GB-RAM-hours, included object
storage, and per-plan PostgreSQL databases, storage and compute. Its two
largest COGS lines, Neon and R2, bill in US dollars, and the marketing ladder
already reads 10 / 30 / 99 / Custom.

## Decision

0. **Currency and prices.** Gregale bills in US dollars only: plan fees, the
   overage meter, credits, refunds, invoices, budgets, the spend cap and the
   object-storage rate card. Plans cost $0 / $10 / $30 / $99 per month and
   overage stays $0.01 per GB-RAM-hour. One constant (`api.BillingCurrency`,
   defined in `pkg/financial`) replaces the hard-coded EUR checks in the
   provider catalog validation, invoice import and the financial ledger. Polar
   products must be recreated with USD prices. Customers' own rate cards (API
   monetization), the AI gateway price catalog and providers' operator cost
   reports keep their own currencies. Metric, alert-metric and preset
   identifiers keep their `eur` names (`meterd_account_spend_eur`,
   `account_spend_eur`, `spend_eur_20`) because dashboards and saved alert
   rules depend on them; their values are in the billing currency. Invoices and
   budgets written in EUR before the switch stay valid; new ones are USD.

1. **Compute.** RAM per app is 256 / 512 / 1,024 / 2,048 MB and included
   GB-RAM-hours are 20 / 200 / 1,000 / 3,000 for Free / Hobby / Pro / Scale.
   Plan RAM is each new app's default and the per-app cap. Existing apps keep
   their stored `ram_mb`, so their snapshots and per-second billing do not
   change until the customer changes the app. The canonical RAM/vCPU shapes
   and the execution memory cap follow plan RAM.

2. **Object storage.** Plans include 1 / 10 / 50 / 200 GiB of average stored
   bytes per billing month (the report's GB, in Gregale's GiB billing units),
   removed before month-close pricing (ADR-1320). Free also stops at 1 GiB
   of account bytes. The report's separate write and read allowances and
   prices are **deferred**: no exporter produces per-class operation counts,
   and the v2 report that carries them is shadow-only (ADR-237, steps 2–3).
   Request and egress allowances stay zero until then. When billing is
   enabled, the operator rate card should follow the report: storage at
   $0.03 per GiB-month and egress at zero.

3. **Managed PostgreSQL.** Plans allow 1 / 1 / 3 / 10 databases, up to the
   development / development / burstable / production class. Storage is
   pooled per account at 0.5 / 1 / 5 / 20 GiB: one database may use the
   whole pool, and the pool is held as the account's monthly storage
   byte-seconds ceiling. Monthly compute is 5 / 25 / 75 / 200 compute-unit
   hours, held as a compute-unit-seconds ceiling. Free gains one development
   database. The restore window stays 7 days on every plan. These are
   ceilings: managed PostgreSQL has no invoicing yet, so the report's
   $0.20/CU-h and $0.60/GB-month overage is not billed. A local dev
   database defaults to the smaller of 1 GiB and the plan's pool.

## Consequences

- Revenue and the larger USD costs (Neon, R2) now share a currency; the FX
  exposure moves to the EUR-billed servers. The report's margins treat $ as €,
  so $10 / $30 / $99 sits at or slightly above its €9 / €29 / €99 basis.
- The Polar sandbox and production catalogs need USD products before
  apid or meterd will start with Polar; the catalog check enforces the
  billing currency.
- Doubling the default RAM halves how many new apps fit on a node. The report
  says the 500-customer capacity needs CPU admission by the cgroup CPU cap
  rather than two guest vCPUs per small app; without it, about 8 active nodes
  are needed instead of 5.
- The Free tier is more generous (4× the GB-hours, one database), which the
  report puts at about 21% of COGS at 4 free users per paying customer.
- The pooled PostgreSQL storage is enforced as a monthly average, not an
  instantaneous size: a database may briefly exceed the pool, and the
  provider's per-database limit (the pool) is the hard size.
- Free stops at its object-storage and PostgreSQL limits; paid plans keep the
  operator safety policy as their storage, request and egress ceiling.

## Not covered

Write/read operation allowances and prices (with exporter qualification),
PostgreSQL overage billing, CPU admission by cgroup CPU cap, and marketing
copy.
