# ADR-20261031165104: Managed PostgreSQL overage is billed

- **Status:** superseded in part by [ADR-20261031110037](20261031110037-managed-postgres-overage-invoicing.md) (2026-11-01); see "Superseded in part" below
- **Date:** 2026-10-31
- **Owner:** apid (managed PostgreSQL) and meterd (billing) maintainers
- **Amends:**
  - [ADR-20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md): the overage ceiling once overage is billed;
  - [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md): the report's PostgreSQL overage is now billed;
  - [ADR-20261026120843](20261026120843-spend-cap-holds-include-live-meters.md): managed PostgreSQL is a live meter under the spend cap.

## Superseded in part (2026-11-01)

[ADR-20261031110037](20261031110037-managed-postgres-overage-invoicing.md) (PR #6157, accepted) now decides compute overage billing. Where the two differ, it wins:

- **Compute.** The same $0.20 per CU-hour, charged up to the existing 2x ceiling (at most one allowance a month). The 4x billed ceiling in decision 1 is withdrawn.
- **Delivery.** `managed_postgres_overage_deliveries` holds the cumulative steps, and the `FAAS_POLAR_MANAGED_POSTGRES_BILLING_*` modes control sending. A month stays open for seven days after it ends. This ADR's progress ledger and its 6 h close are withdrawn.
- **Spend cap.** The charge does not count toward the spend cap (that ADR's decision 7). This ADR's spend-cap hold for databases is withdrawn.

The step 1 and step 2a PRs (#6211, #6216) were closed unmerged.

Still open from this ADR:

- storage past the pool at $0.60 per GiB-month, to be added on the deliveries ledger by an amendment to ADR-20261031110037;
- the always-on add-on, which has no price yet.

## Context

- **The rates are set but not charged.** Managed PostgreSQL launches GA with the public release (ADR-20261031091607, PR #6126). Its public-release gate includes PG-02 of the epic (#6146): a priced way to keep a database running past its monthly allowance.
  - The owner chose billed overage at ADR-1430's rates: $0.20 per CU-hour and $0.60 per GB-month.
  - An always-on add-on is also planned. It has no price yet and is out of scope here.
- **Overage is unbilled today, so it is capped hard.**
  - With overage on, a paid account's databases run to twice the monthly compute allowance and are then suspended.
  - The storage pool doubles.
  - None of it is billed, and the `postgres-overage` capability is `internal`.
- **A production database cannot last the month.**
  - A production-class database never scales below 1 CU.
  - Scale includes 200 CU-hours, so steady traffic spends the allowance in about 8 days and the 2× ceiling in about 17. A month needs 720 CU-hours.
- **Usage is already measured.**
  - The usage collector records each database's compute-unit seconds and storage byte-seconds per window from Neon's consumption API (ADR-516).
  - `ManagedPostgresUsageForSubject` sums them per account or organization, leaving out windows in a delete's undo period (ADR-20261029174950).
  - A restore's consumption is reported inside its source's Neon project, so a restored copy is never measured twice.
- **The billing pattern exists.** Object storage already bills a resource meter inside its month: daily month-to-date steps, a compare-and-set ledger, one Polar event per step, and spend-cap headroom taken as it bills (ADR-20261027093220).

## Decision

1. **What is billed.** These apply only for a paid account or organization with overage on, for the time it is in effect. Free never pays overage; it is suspended at its allowance.
   - **Compute.** CU-seconds past the month's compute allowance at $0.20 per CU-hour: `api.ManagedPostgresComputeOverageMillicentsPerCUHour = 20_000`.
     - The allowance follows time on each plan (ADR-20261030093512).
   - **Storage.** Byte-seconds past the pool held for the month so far at $0.60 per GiB-month: `api.ManagedPostgresStorageOverageMillicentsPerGiBMonth = 60_000`.
     - One GiB-month is 2³⁰ bytes held for every second of that UTC month.
   - **Integer millicents, cumulative.** The charge is computed from cumulative usage and floored once:
     - `floor(compute_over_s × 20 000 / 3 600)`;
     - plus `floor(storage_over_byte_s × 60 000 / (2³⁰ × month_s))`.
     A step bills the difference from what was already delivered, so rounding never compounds.
   - **Not billed:**
     - windows in a delete's undo period;
     - the storage of a restored copy holding the restore slot (ADR-20261031123259, PR #6142), which the pool leaves out. Its compute is billed like any database's.
2. **Daily month-to-date steps.** These are object storage's mechanism (ADR-20261027093220 decisions 1–3) for a new ledger, `managed_postgres_billing_progress`. It holds one row per provider, subject, UTC month and mode.
   - An hourly apid sweep delivers each subject's month-to-date charge at most once every `api.ManagedPostgresBillingProgressInterval` (24 h).
   - Each step goes through the same four compare-and-set writes: reserve, fix, publish, complete.
   - A subject whose usage is not fresh (the collector's freshness, as `postgres usage` reports it) is skipped until it is. Stale usage never bills.
   - **Month close.** It runs once the month's usage has settled, `api.ManagedPostgresBillingSettle` (6 h) after the month ends. That is past the collector's correction tail (ADR-516).
     - One last step brings the month up to the record's total, and a receipt records the cumulative quantities.
     - As with object storage, a subject no longer on a paid plan gets no new charge. It only finishes a step that was already fixed.
3. **The event.** Each live step sends one `faas_managed_postgres_usage` event to Polar.
   - `charge_millicents` is the step's delta, and the event is timestamped when it is sent.
   - The external ID is `faas-managed-postgres-<subject>-<YYYY-MM>-<target millicents>`.
   - Metadata carries the month, the cumulative charge, and the cumulative billed CU-seconds and GiB-month.
   - The provider configuration gains:
     - `managed_postgres_billing_mode` (`off`, `shadow`, `live`; default `off`);
     - `managed_postgres_billing_from`;
     - `managed_postgres_usage_event_name`;
     - `managed_postgres_meter_id`.

     A meter that sums `charge_millicents` keeps the integer ledger exact, as object storage's does.
4. **The spend cap.** Managed PostgreSQL overage takes the account's spend-cap headroom as it is billed, as object storage does (ADR-20261027093220 decision 4).
   - A step's cumulative target is `min(month-to-date charge, cap − billable compute − billable egress − billable object storage)`.
   - meterd reserves the month's live PostgreSQL charge before it clamps compute and egress, and counts the delivered charge toward the spend-cap hold. Apps park when the combined total reaches the cap.
   - **New:** the cap enforcer treats a spend-cap hold for the current month as the compute ceiling reached, so the subject's databases are suspended too.
     - At the cap, apps and databases stop together.
     - Raising the cap lifts both at the next cap sweep.
     - The cap email (ADR-20261025094915 decision 8) names the spend cap as the reason.
5. **The ceiling once overage is billed.** With `managed_postgres_billing_mode = live`, the compute ceiling is four times the allowance (`api.ManagedPostgresBilledOverageCeilingMultiplier = 4`) instead of twice.
   - On Scale that is 800 CU-hours, enough for one production-class database at its 1 CU floor for the whole month.
   - The customer's control is the spend cap; the 4× ceiling is the platform's backstop.
   - The storage pool stays at twice the plan's pool. Storage past it blocks writes, as today.
   - In `off` and `shadow` mode, nothing changes: 2× and unbilled.
6. **The customer surface:**
   - `gregale postgres usage` and the usage API show the month-to-date overage charge, what it is for, and the spend cap it counts against.
   - `gregale postgres overage on` states the rates and the cap.
   - The dashboard shows the same.
   - `postgres-overage` moves from `internal` to `ga` once live billing is on in production.
   - The customer guide documents how to keep a production database running for a whole month: overage on, and a spend cap that covers it. At 1 CU for 720 hours on Scale, that is (720 − 200) × $0.20 = $104.
7. **Reconciliation with Neon.**
   - At month close, apid compares the month's billed and waived quantities with the provider's consumption totals for each backend's organization for the same month.
   - The difference is exported as `apid_managed_postgres_billing_reconciliation_drift_ratio`.
   - `FaasManagedPostgresBillingDrift` warns above 2%.
   - The provider-cost evidence already kept for accounting (ADR-591) is what the alert links to.
8. **Rollout.**
   - Turn mode `off` → `shadow` for one whole month. Compare shadow charges, our provider-cost ledger, and Neon's invoice.
   - Then turn it `live`.
   - An N−1 apid has no ledger and sends nothing, but keep the mode `off` or `shadow` until every apid runs this release, as object storage does.

## Delivery

1. **Shadow billing:**
   - the pricing constants, the ledger migration and the shadow sweep;
   - the charge in `postgres usage` and the dashboard;
   - metrics.
2. **The cap:**
   - meterd's reservation and the spend-cap hold;
   - the cap enforcer suspending at a hold;
   - the 4× ceiling under live mode;
   - the cap email's reason.
3. **Live billing:**
   - the live publish, month close and receipt;
   - the reconciliation metric and alert;
   - the operator runbook;
   - the customer docs;
   - `postgres-overage` → `ga`.

## Consequences

- **A full month is possible.** A paid customer can keep a production database running for the whole month by choosing overage and a spend cap that covers it, instead of being suspended on day 8 or 17.
- **The default spend caps still bound it.** Paid plans start with a cap of one more app allowance: Hobby $2, Pro $10, Scale $30. On Scale that is 150 CU-hours of PostgreSQL overage. A customer who wants more raises the cap; nothing is charged without that choice.
- **Billing follows provider data.** It is billed from the same ledger the cap enforcer reads, so the bill follows provider data; drift from Neon shows up as an alert, not a silent loss.
- **Churn.** A customer who churns is billed up to their last daily step. At most one interval of the final month goes unbilled.

## Rejected alternatives

- **Bill at month close only.** It has the churn gap object storage had (Hunt 19 H19-1).
- **Bill the provider's cost plus a margin.** The bill would move with Neon's prices; the financial model sells fixed rates.
- **Remove the ceiling once overage is billed.** Then a runaway workload under a raised cap has no platform backstop, and the fleet spend breaker (ADR-20261028103115) becomes the only bound.
- **Keep 2× when billed.** A production database still stops around day 17 on Scale, which is what PG-02 must fix.

## Open for the owner

1. **The billed ceiling multiplier.** 4× is proposed. A larger value or none moves the whole bound to the spend cap.
2. **The default spend caps.** They stay as they are: one more app allowance. Raising them for accounts with managed PostgreSQL would make a full production month the default, not a choice.
