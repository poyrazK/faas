# ADR-20261102072425: Managed PostgreSQL overage is the provider's price plus 5%

- **Status:** accepted (owner, 2026-11-02)
- **Date:** 2026-11-02
- **Owner:** apid (managed PostgreSQL) and billing maintainers
- **Amends:**
  - [ADR-20261031110037](20261031110037-managed-postgres-overage-invoicing.md): the compute overage price;
  - [ADR-20261031165104](20261031165104-managed-postgres-overage-billing.md): the storage overage price it left open;
  - [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md): the COGS report's managed PostgreSQL overage rates.

## Context

- **Gregale runs on Neon's Scale plan, and stays there.**
  - Every Gregale database is its own Neon project. Launch allows 100 projects; Scale allows 1,000, and the limit can be raised.
  - Scale also brings an uptime SLA, HIPAA, IP allow rules, private networking, metrics export and a 30-day history window.
- **Scale costs more for compute only.** On neon.com/pricing (2026-11-02) both plans charge the same:
  - storage: $0.35 per GB-month;
  - restore history: $0.20 per GB-month;
  - egress: $0.10 per GB past 500 GB.

  Compute is the difference: $0.106 per CU-hour on Launch and $0.222 on Scale.
- **The current price sells below cost.** ADR-20261031110037 bills overage at the October COGS report's $0.20 per CU-hour, so every billed overage hour loses $0.022 on Scale. Storage overage (ADR-20261031165104, still open) was planned at $0.60 per GiB-month.

## Decision

1. **Managed PostgreSQL overage costs what the provider charges Gregale, plus 5%.**
   - `price = provider price × (100 + api.ManagedPostgresOverageMarkupPercent) / 100`, in integer millicents.
   - `api.ManagedPostgresOverageMarkupPercent = 5`.
2. **Compute.**
   - `api.ManagedPostgresNeonScaleMillicentsPerComputeUnitHour = 22_200` ($0.222).
   - Overage is `api.ManagedPostgresOverageMillicentsPerComputeUnitHour = 23_310`, which is $0.2331 per CU-hour.
   - The ceiling is unchanged, so a month of overage costs at most one allowance: Hobby $5.83 (25 CU-h), Pro $17.48 (75), Scale $46.62 (200).
3. **Storage past the pool, when it is billed.**
   - Neon's $0.35 per GB-month becomes $0.3675 per GB-month, replacing ADR-20261031165104's $0.60.
   - The storage billing change converts it to the ledger's unit from Neon's own definition of a GB.
4. **A provider price change moves the cost constant, and the price follows.**
   - Charges are cumulative per UTC month, so a change applies to the whole month its release goes live in.
   - Ship it before the month it is for.
   - Live billing starts at a month boundary (`FAAS_POLAR_MANAGED_POSTGRES_BILLING_FROM`), so this change reprices nothing already delivered.

## Why

Gregale sells compute (the pricing principle): the provider's cost passes through with a thin margin and never below cost. Scale's extra cost buys the project count that one-project-per-database needs, and the compliance and networking features production customers ask for.

## Consequences

- The overage margin is 5% of the provider's price. It no longer depends on a COGS estimate.
- The business model (`ex44_faas_financial_model.xlsx`) and the COGS report's managed PostgreSQL rows need the owner's update to these figures.
- **Still open:**
  - The always-on add-on has no price yet.
  - The restore-history cap is undecided. Neon charges $0.20 per GB-month of history.

## Rejected alternatives

- **Keep $0.20 per CU-hour.** It loses $0.022 on every billed hour.
- **Move to Neon Launch ($0.106).** It caps the fleet at 100 databases, and has no SLA, HIPAA or private networking.
- **Price against Supabase.** Its always-on instances work out to about $0.08 per CU-hour equivalent, below what Neon charges Gregale.
