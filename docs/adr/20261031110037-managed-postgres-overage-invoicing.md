# ADR: Managed PostgreSQL overage invoicing

Date: 2026-10-31
Status: Accepted
Amends: [ADR 20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md) (decision 2: overage was unbilled until invoicing)
Related: [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md) (plan entitlements, COGS report), [ADR 20261027093220](20261027093220-object-storage-billed-daily.md) (object-storage charge steps), [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md) (spend cap)

## Context

ADR 20261025094915 lets a paid account turn managed PostgreSQL overage on:
its databases then keep running past the monthly compute allowance, up to
twice it. It left that compute unbilled "until managed PostgreSQL has
invoicing", with the COGS report's $0.20 per CU-hour as the price to use.
Because of that, the `postgres-overage` capability cannot be released: each
account that turns it on gets up to one extra allowance of compute a month
for free.

Object storage already reaches Polar as cumulative charge steps on a
millicent-unit meter, delivered by apid and keyed by the cumulative total
(ADR 20261027093220). Managed PostgreSQL usage, its cap sweep and the
overage setting all live in apid.

## Decision

1. **Price.** Managed PostgreSQL compute above the plan's monthly allowance
   costs $0.20 per CU-hour (`api.ManagedPostgresOverageMillicentsPerComputeUnitHour`
   = 20,000 millicents). It is charged per compute-unit second, rounded down to
   a whole millicent. The charge stops at the ceiling (twice the allowance), so
   a month costs at most one allowance: Hobby $5, Pro $15, Scale $40. Free has
   no overage.
2. **Who is charged.** A subject (an account, or an organization that owns
   resources) is charged for a month when the cap sweep recorded its
   `allowance` event in that month and overage was on at that time, is on now,
   or the month already has a delivered charge. A subject that never turned
   overage on is never charged, even if it overshot its allowance between two
   sweeps.
3. **What is charged.** The ledger's compute for the month
   (`UsageSnapshot.ComputeUnitSeconds`) above the allowance. The allowance is
   the larger of the plan's current one and the one recorded with the
   `allowance` event, so an upgrade later in the month never raises the
   charge.
4. **Delivery.** After each cap sweep, apid computes each subject's
   cumulative month charge. When it exceeds the delivered total, apid sends
   the difference as one Polar event on a fourth meter
   (`faas_managed_postgres_usage`, summing `metadata.charge_millicents`, priced
   at $0.00001 per unit). The external ID is
   `faas-managed-postgres-<subject>-<month>-<cumulative>`, so a retry after an
   ambiguous response is a duplicate. apid then advances
   `managed_postgres_overage_deliveries` from the previous total only (a
   compare-and-set), so two replicas cannot record one step twice. The
   delivered total never goes down.
5. **Month close.** For seven days after a UTC month ends
   (`api.ManagedPostgresOverageCloseWindow`), apid also brings the previous
   month up to date as late provider usage windows reach the ledger. After
   that, the month is closed.
6. **Rollout.** `FAAS_POLAR_MANAGED_POSTGRES_BILLING_MODE` is `off` (the
   default), `shadow` (record each step locally, send nothing) or `live`.
   `FAAS_POLAR_MANAGED_POSTGRES_BILLING_FROM` names the first UTC month
   charged. `FAAS_POLAR_MANAGED_POSTGRES_METER_ID` names the meter. The Polar
   catalog preflight checks that the meter and every paid product's price
   match. Charges need compute cap enforcement `on`, because the cap sweep
   records the `allowance` event.
7. **Spend cap.** The charge does not count toward the account's spend cap
   (ADR-1320). The overage setting is its own opt-in with its own hard
   ceiling: the databases are suspended at twice the allowance.

## Consequences

- Releasing `postgres-overage` no longer gives compute away once delivery is
  `live`. Release order: Polar meter and prices, then `shadow` for a month,
  then compare receipts with the ledger, then `live`, then the capability.
- The month's charge follows the ledger, which lags the provider by its
  collection window, and the close window covers late windows. Usage the
  ledger learns of more than seven days after the month ends is not charged.
- An invoice can show managed PostgreSQL overage beside compute overage and
  above the spend cap, because of decision 7. The docs say so.
- Polar invoices on the subscription cycle, not the UTC month, so a step
  appears on the invoice after its delivery. The same is true of the other
  meters.
- The dashboard and `gregale postgres usage` do not show the charge yet. The
  usage response can add it later as an `omitempty` field.
