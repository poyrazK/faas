# ADR-20261123062111: Managed PostgreSQL restore history is billed at the provider's price plus 5%

- **Status:** accepted (owner, 2026-11-23: option B, with D1–D3 as recommended). Charging is implemented; D2 (the shorter restore window for accounts with overage off) is implemented behind `FAAS_MANAGED_POSTGRES_HISTORY_LIMIT` (default shadow), without its email.
- **Date:** 2026-11-23
- **Owner:** apid (managed PostgreSQL) and billing maintainers
- **Closes:** the open item "The restore-history cap is undecided" in
  [ADR-20261102072425](20261102072425-managed-postgres-overage-at-provider-price-plus-5.md).
- **Amends:**
  - [ADR-20261031110037](20261031110037-managed-postgres-overage-invoicing.md): the overage step gains a restore-history charge;
  - [ADR-20261102072425](20261102072425-managed-postgres-overage-at-provider-price-plus-5.md): the pricing principle covers restore history and snapshots.

## Context

- **Neon bills Gregale for two kinds of history** (neon.com/pricing, last updated 2026-11-04):
  - instant-restore history (the WAL behind point-in-time restore): $0.20 per GB-month;
  - snapshot storage: $0.09 per GB-month.

  Branch data is $0.35 per GB-month. Neon's GB-month is reported as byte-months normalised to a 744-hour month (`byteMonthsToSeconds` in `pkg/managedpostgres/neon/usage.go`).
- **Gregale meters both, and charges neither.**
  - `pkg/managedpostgres/neon/usage.go` adds `instant_restore_bytes_month` and `snapshot_storage_bytes_month` into one meter, `MeterHistoryByteSeconds`. `gregale postgres usage` shows it as restore history.
  - `pkg/managedpostgres/overage_billing.go` charges compute, branch storage past the pool (`StorageOverageCharge`) and database egress. History is not part of the step.
- **Nothing limits history for a customer.**
  - The plan's storage pool is enforced through Neon's branch size limit, which counts branch data only.
  - `UsagePolicy.MaxMonthlyHistoryByteSeconds` is an operator-wide safety ceiling that suspends a database. It is `0` (off) in the shipped configs, and it is not a plan limit.
- **History size follows write volume, not data size.** A database that rewrites 1 GiB of rows every day, with the 7-day restore window every plan has, keeps about 7 GiB of history for 1 GiB of data. A customer cannot see or shrink it, except by deleting the database.
- **This is the only cost a pricing review found Gregale paying and not passing on.** Every other managed PostgreSQL meter is priced at or above Neon's cost. Prices are net of tax since 2026-11-23 (Polar tax behaviour exclusive), so the rates below are what Gregale receives.

## Options

### A. Count history toward the storage pool and storage overage

History bytes are added to branch storage before `StorageOverageCharge`; past the pool, they are charged at $0.50 per GiB-month.

- For: no new price, meter or delivery column.
- Against:
  - $0.50 is 2.5× what Neon charges for restore history and 5.6× for snapshots. That breaks the cost-plus-5% principle in the customer's disfavour.
  - The pool is enforced by Neon's size limit, which ignores history. A Hobby database with 0.5 GiB of data and 3 GiB of history would be "over the pool" while its writes still succeed, and without overage the charge never happens. The pool would mean two different things.
  - The overage ceiling is one more pool (Hobby $0.50, Pro $2.50, Scale $10 a month), so heavy history past two pools would still be unpaid.
  - `usage` would no longer show storage as the provider reports it.

### B. A separate history rate at the provider's price plus 5% (recommended)

History past a plan-included amount is charged on the existing overage step, at Neon's price plus `api.ManagedPostgresOverageMarkupPercent`.

- For:
  - It follows the pricing principle exactly, as compute and egress already do.
  - It closes the loss for every account that has overage on, with no new limit for customers to hit.
  - Delivery, idempotency, the close window, plan spans and the spend cap already exist for the step.
- Against:
  - A new meter (snapshots split from restore history), a delivery column and a Polar metadata field.
  - Accounts with overage off still need an answer (D2).

### C. A per-plan history cap

Each plan gets a monthly history ceiling (for example, its storage pool). At the ceiling the subject is held, the way `MaxMonthlyHistoryByteSeconds` holds a database today.

- For: bounded cost with no new price.
- Against:
  - Holding a database because of yesterday's writes is a severe, surprising response; history only shrinks as the restore window slides, so the hold cannot be cleared by the customer.
  - It earns nothing from customers who would happily pay for heavy history.
  - It leaves the margin at zero below the cap and makes the cap the product decision instead of the price.

## Recommendation: B

### 1. Prices, in `pkg/api/limits.go`

```go
// What Neon's Scale plan charges Gregale per GB-month (10^9 bytes, a
// 744-hour month; neon.com/pricing, last updated 2026-11-04).
const ManagedPostgresNeonMillicentsPerRestoreHistoryGBMonth = 20_000 // $0.20
const ManagedPostgresNeonMillicentsPerSnapshotGBMonth = 9_000         // $0.09

// Prices past the plan's included history: provider + markup.
const ManagedPostgresRestoreHistoryOverageMillicentsPerGBMonth =
	ManagedPostgresNeonMillicentsPerRestoreHistoryGBMonth * (100 + ManagedPostgresOverageMarkupPercent) / 100 // 21_000, $0.21
const ManagedPostgresSnapshotOverageMillicentsPerGBMonth =
	ManagedPostgresNeonMillicentsPerSnapshotGBMonth * (100 + ManagedPostgresOverageMarkupPercent) / 100 // 9_450, $0.0945
```

- **Unit.** A GB is 10^9 bytes and a month is 744 hours, Neon's own units, as egress already uses. The charge is `floor(billed_byte_seconds × rate / (10^9 × 2 678 400))`, which equals Neon's byte-months × rate. If Neon's GB were ever 2^30 bytes, charging per 10^9 bytes would still be above cost, never below.
- **Two meters.** `MeterHistoryByteSeconds` keeps instant-restore history only. A new `MeterSnapshotByteSeconds` takes `snapshot_storage_bytes_month`. Folding both at $0.21 would overcharge snapshots 2.3×; folding both at $0.0945 would sell restore history below cost.

### 2. Included history (D1)

Each plan includes history equal to its storage pool, held for the whole month: Hobby 1 GiB, Pro 5 GiB, Scale 20 GiB. It covers restore history first, then snapshots, so the more expensive meter is absorbed first.

- Its cost to Gregale is at most Hobby $0.21, Pro $1.07, Scale $4.29 a month (2.1%, 3.6% and 4.3% of the $10, $30 and $99 plan fees).
- Without it, any database that writes would carry a few cents of history overage every month, and the default-off Hobby and Pro accounts would hit D2's response under normal use.
- `api.ManagedPostgresIncludedHistoryBytes(pool)` returns the plan's pool, so a pool change moves it.

### 3. Charging, on the existing step

- **Who.** A subject with managed PostgreSQL overage on, the same switch as compute and storage overage. A month that already charged history keeps counting it after overage is turned off, as storage does (ADR-20261031110037 amendment 2).
- **What.**
  - `included = included_bytes × 2 678 400` byte-seconds;
  - `history_billed = max(history_byte_s − included, 0)`;
  - `snapshot_billed = max(snapshot_byte_s − max(included − history_byte_s, 0), 0)`;
  - `millicents = floor(history_billed × 21 000 / (10^9 × 2 678 400)) + floor(snapshot_billed × 9 450 / (10^9 × 2 678 400))`.
- **No 2× ceiling.** Unlike compute and storage, Neon puts no limit on history, so a ceiling would bring the loss back past it. The month's charge is bounded by the subject's spend cap (`CapRoom`), as every live step already is. At the cap the databases are held with `hold: compute_ceiling`, as today.
- **Plan spans.** Each span bills against its own plan's included history, as compute does (ADR-20261103131742 amendment 2).
- **Row and event.** The delivery row and step add `billed_history_byte_seconds` and `billed_snapshot_byte_seconds` (one migration, CHECKs added `NOT VALID`). The Polar event metadata adds the history and snapshot usage, the included bytes, the billed byte-seconds and the two prices.
- **Start.** The first whole UTC month after release, through a new `FAAS_POLAR_MANAGED_POSTGRES_HISTORY_BILLING_FROM`, so no month already delivered is repriced. Managed PostgreSQL billing is already live, so the shadow month is the ledger: compare one whole month of `history_byte_seconds` and `snapshot_byte_seconds` with Neon's invoice before setting it (apid also logs `managed postgres restore history not billed yet` at debug level). The month must start after the release reached production: earlier windows hold snapshot storage in `history_byte_seconds`.

### 4. Accounts with overage off (D2)

History cannot be refused at write time, so an overage-off subject still costs Gregale its history. Recommended: once a subject's month-to-date history and snapshots pass **twice** its included amount, apid shortens the restore window of its databases to `api.ManagedPostgresHistoryLimitedRestoreWindow = 24h` until the 1st of the next month, or until overage is turned on.

- Neon drops history older than the window, so the cost stops growing. Writes, reads and data are untouched.
- `usage` shows `history_state: limited`, and the account is emailed once that month, naming the restore points it lost and `gregale postgres overage on` as the way back.
- The unbilled cost is at most one more included amount past what the plan covers: Hobby $0.21, Pro $1.07, Scale $4.29 a month.

Alternatives for D2: absorb the cost (unbounded); bill history regardless of the overage switch (breaks "without overage nothing is charged" in `docs/postgres.md`); hold the subject (option C's problem).

### 5. Docs

`docs/postgres.md` (Plans table and Usage and limits) and `docs/plans.md` (a managed PostgreSQL section, generated by `cmd/pricing-md` from `pkg/api`) state the included history, the two rates and the overage-on rule in the same words. D2's limited state is documented with D2. `docs/postgres.md` today says "Storage is a limit" and does not mention the $0.50 per GiB-month storage overage that ADR-20261031110037 amendment 2 bills; the same change corrects it.

### Worked example

Pro, overage on, one month: average restore history 12 GB, snapshots 1 GB.

- Included: 5 GiB = 5.369 GB, all taken by restore history.
- Restore history billed: 6.631 GB × $0.21 = $1.39.
- Snapshots billed: 1 GB × $0.0945 = $0.09.
- Charge: $1.49 on top of compute, storage and egress overage. Neon charges Gregale $2.49 for the month's history; the plan fee covers the included $1.07.

## Decisions (owner, 2026-11-23)

- **Option:** B.
- **D1, included history:** the plan's storage pool.
- **D2, overage off:** shorten the restore window to 24 h past twice the included amount.
- **D3, start month:** the first whole month after release, after one logged month.

## Implementation

- **Prices and limits** in `pkg/api/limits.go`: the two Neon cost constants, the two prices derived with `ManagedPostgresOverageMarkupPercent`, `ManagedPostgresHistoryMonthSeconds` (744 h) and `ManagedPostgresIncludedHistoryBytes`.
- **Meter.** `MeterSnapshotByteSeconds` (`snapshot_byte_seconds`) takes Neon's `snapshot_storage_bytes_month`; `history_byte_seconds` keeps `instant_restore_bytes_month` only. Windows recorded before the release keep both in `history_byte_seconds`, so history billing starts in a month after the release. The operator's `max_monthly_history_byte_seconds` ceiling still bounds both together, and the cost estimate prices snapshots at the history rate, an upper bound. The operator usage line items add `managed_postgres.snapshots`.
- **Charge.** `HistoryOverageCharge` and the plan-span sum in `pkg/managedpostgres`. A step never lowers the history the month already billed.
- **Schema.** Migration `20261123105120100_managed_postgres_history_billing` widens the usage meter CHECK online and adds the delivery and reservation columns.
- **Customers.** `gregale postgres usage` and the usage API show `snapshot_byte_seconds` and `included_history_bytes`.
- **D2 (2026-11-24).**
  - **Sweep.** A history pass in the cap sweep (`pkg/managedpostgres/history_limit.go`), counted apart from the compute and storage passes. Per subject it compares month-to-date `history_byte_seconds + snapshot_byte_seconds` from the ledger with `api.ManagedPostgresHistoryLimitFactor` (2) times the included history held for 744 hours. Over it with overage off, each provider object the subject's ready databases live in gets `min(spec window, api.ManagedPostgresHistoryLimitedRestoreWindow)` (24 h); otherwise it gets its spec window back. The reset on the 1st and the lift on overage on need no state of their own: the next sweep sees the subject under the limit. The pass only moves a window between those two values; any other observed window is drift it logs and leaves.
  - **Provider.** The optional `HistoryRetentionProvider`; Neon PATCHes the project's `history_retention_seconds` and confirms it by reading it back. It never writes zero (PITR off) or more than the backend's maximum.
  - **Drift.** `SpecForObservation` reads the limited window below the spec's as the spec, as it reads a pool-lowered storage limit, so a limited database is not `spec_mismatch` and still resizes.
  - **Restores.** `Service.RestoreWithResult` reads the source's provider window and refuses a point older than the limited one before reserving a target, naming the limit and the way back.
  - **Usage.** `history_state` is `limited` or `normal`, present only while the mode is `on`; `gregale postgres usage` prints it.
  - **Rollout.** `FAAS_MANAGED_POSTGRES_HISTORY_LIMIT` is off, shadow (the default) or on. Turn it on only after the `postgres-overage` capability is released, because overage on is a limited customer's way back.
  - **Not built.** The once-a-month email: production has no internal mail on the control plane. Until it ships, a limited customer learns of it from `usage` and from a refused restore.

## Consequences

- Every managed PostgreSQL cost Neon bills Gregale is passed on at cost plus 5%, or covered by the plan fee up to the included amount.
- History is no longer silently unbounded for overage-off accounts; the price of that bound is a shorter restore window, which the customer is told about and can lift.
- The business model and the COGS report's managed PostgreSQL rows need the owner's update for history revenue.
- A Neon price change moves the two cost constants, and the prices follow, as ADR-20261102072425 decision 4 already says for compute.
