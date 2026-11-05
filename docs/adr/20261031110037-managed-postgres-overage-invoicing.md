# ADR: Managed PostgreSQL overage invoicing

Date: 2026-10-31
Status: Accepted; price amended by [ADR-20261102072425](20261102072425-managed-postgres-overage-at-provider-price-plus-5.md) ($0.2331 per CU-hour, Neon Scale's price plus 5%)
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
   charge. Amendment 3 resolves the plan allowance from the billed month's
   recorded activations rather than today's plan.
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

## Amendment 1: idempotent on the starting point; shadow and live apart (2026-11-02)

Bug hunt 33 found two ways decision 4 could charge the wrong amount.

- **Double charge after an ambiguous response.** The external ID named the
  cumulative target. Usage keeps growing between sweeps, so after a publish
  Polar accepted but whose response was lost (a timeout, apid restarting),
  the next sweep's step had a new target, a new external ID, and charged
  from the same delivered amount again. The external ID now names the step's
  starting point: `faas-managed-postgres-step-<subject>-<month>-from-<delivered>`.
  A retry starts from the same point, so Polar keeps one event. The cost is
  a possible undercharge of the usage between the two attempts, which only
  happens after an ambiguous response; an overcharge cannot happen.
- **Shadow rows counted as charged.** Shadow and live share the month's row.
  A live step now starts from zero over a shadow row and replaces it; a
  shadow step never rewrites a live row. The record compares both the
  amount and the mode it read.

## Amendment 2: storage past the pool is billed (2026-11-02)

ADR-20261031165104 left storage overage open. [ADR-20261102072425](20261102072425-managed-postgres-overage-at-provider-price-plus-5.md) priced it at $0.50 per GB-month. It is now billed on the same delivery row and step as compute:

- **Who.** A subject with overage on. With overage on its storage pool doubles (ADR-20261025094915 decision 9), so storage past the plan's pool is billed instead of failing writes. Without overage, storage is never charged.
- **What.** Storage held past the plan's pool, averaged over the UTC month:
  - `billed = min(max(storage_byte_s − pool × month_s, 0), pool × month_s)`;
  - `millicents = floor(billed × 50 000 / (2³⁰ × month_s))`, using `api.ManagedPostgresStorageOverageMillicentsPerGiBMonth`.

  Storage is metered in GiB, the unit of the plans' pools. The month's whole pool is the allowance, as object storage does it, so a step never shrinks as the month goes on. At most one more pool is charged a month: Hobby $0.50, Pro $2.50, Scale $10.
- **Usage.** `UsageSnapshot.StorageByteSeconds`, the provider's measured storage for the subject. A restored copy's storage is reported inside its source's project, so pages it shares with the source are counted once.
- **One charge.** The step's target is compute plus storage, cumulative. The row records `billed_storage_byte_seconds` (migration `20261102085140417`, its CHECK added `NOT VALID`), and the Polar event's metadata adds the storage usage, pool, billed byte-seconds and price.
- **Subjects.** `ManagedPostgresOverageSubjects` also lists subjects with overage on and subjects already charged in the month, besides those that reached the compute allowance.
- **Turning overage off.** A month that charged storage keeps counting it, so the cumulative charge never drops and later compute is not hidden behind it.
- **Skew.** The previous release's upsert does not name the new column, so its rows keep 0. Its compute-only target never exceeds a combined one, so it delivers nothing until compute grows past it. The total stays cumulative.

## Amendment 3: plan changes do not reprice closed months (2026-11-04)

The seven-day close window previously read today's plan for last month's
usage. A November downgrade from Scale to Hobby could therefore charge
October's included storage or compute, even when October never reached its
compute allowance and had no cap event retaining the larger grant.

- Billing resolves the subject's compute and storage allowances separately
  for each UTC month, using meterd's existing `financial_price_snapshots`
  compute-plan activations. These cover personal and organization subjects,
  including subjects without app compute. Each allowance is the largest of
  that month's recorded plans; the month in progress also includes today's
  plan before meterd records its activation. Activations at or after the
  billed boundary are excluded. A closed month never reads a later plan as
  its allowance.
- Compute still preserves a larger allowance recorded by that month's cap
  event. Prices, overage opt-in rules, cumulative delivery and ceilings are
  unchanged.
- A closed month with usage but no plan history is deferred, logged as a
  billing failure and retried within the close window. Missing history must
  be restored before that window ends. Empty months require no history.
- An immutable reserved step already retains its allowances. It is replayed
  unchanged even when history is temporarily unavailable; a deleted subject
  is still skipped.
- No schema change is needed. During a rolling release, an older biller
  still uses today's plan; complete the control-plane rollout before
  relying on historical billing protection.

## Amendment 4: opting out preserves pending storage delivery (2026-11-05)

Monthly billing subject discovery includes reserved steps even when the subject
has since disabled overage, has no delivery receipt yet, and never reached its
compute allowance. A storage-only publish whose response or local receipt was
lost must remain discoverable on the next pass. Both catalog and memory stores
retain this independent recovery source. The original pending payload is
replayed before new-charge eligibility or monthly-allowance checks, including
across a UTC month boundary within the existing close window. Prices, opt-in
rules for new charges, the close window and provider idempotency are unchanged.


## Amendment 5: reserved settlement survives month close (2026-11-05)

The seven-day window limits new charges for late provider usage. It cannot
abandon an immutable step that may already have reached the billing provider.
Every delivery pass first pages that provider's pending reservations through
the current UTC month, including older months and reservations preceding a
changed effective-from setting. A failed older entry does not starve later
pages. Other providers and future months are excluded.

Recovery sends the exact stored payload and completes its receipt with the
existing atomic compare-and-set. Missing plan history does not alter it; a
deleted billing subject is still skipped. Live reservations cannot complete in
shadow mode. A subject/month attempted in recovery is skipped during the normal
monthly pass, so that pass neither resends it nor immediately charges growth
above it. New usage outside the close window remains unbilled. A concurrent
index on `(provider, period_start, subject_id)` supports the recovery cursor.
