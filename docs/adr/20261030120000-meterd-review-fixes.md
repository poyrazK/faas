# ADR-20261030120000: meterd review fixes: settled rolls, a 72 h catch-up window, cheap cap reads

- **Status:** accepted
- **Date:** 2026-10-30
- **Owner:** billing maintainers
- **Amends:** ADR-790 (catch-up window), ADR-1162 (finality bound), ADR-20261029180000 (spend cap cost, dunning alert)
- **Related:** ADR-566 (price snapshots), ADR-060 / ADR-072 (capacity floors)

## Context

A code review of meterd on main 21b34d9578 (2026-10-30) found eight issues
in the billing paths:

1. The spend-cap hold summed a month of `usage_minutes` per capped account
   every quota tick (`CurrentMonthOverageCents`). The same query shape cost
   226 ms per call on production-us before `UsageByMonth` moved to
   `usage_month_totals`. Since ADR-20261029180000 every paid account has a
   cap.
2. A failed billing-account read let the sampler continue with an empty plan
   map, and that map names each row's billing subject: a company app's minute
   was written, first-write-wins, to its creator's account.
3. The quota ladder compared the month's usage with the current plan only,
   while billing removes the largest allowance the month's plans granted. A
   Pro account downgraded to Hobby got daily overage warnings with nothing
   billed as overage.
4. `Dunning.RunOnce` logged a failed suspension or deletion and returned nil,
   so `FaasMeterdDunningFailing` saw only a failed account list.
5. A minute was rolled as soon as it closed. Residency intervals are stamped
   with `clock_timestamp()` inside their writer's transaction and become
   visible at commit, so a transition still committing at the boundary was
   missed for that minute, and first-write-wins kept the short row.
6. Catch-up reached back 24 h. A longer meterd outage lost the older usage for
   good, because the pusher then treats those hours as final.
7. A persistent price-capture failure for one account withholds the
   compute-complete record for every account.
8. A caught-up minute took its deployment floor from instances' state now,
   and a failed deployment read dropped the floor silently.

## Decision

1. **The spend-cap read uses month totals.** `CurrentMonthOverageCents` reads
   `usage_month_totals` for tracked months, exactly as `UsageByMonth` does.
2. **No billing subject, no roll.** A failed billing-account read fails the
   minute's roll; a later tick rolls it with the right subject.
3. **Paid warnings follow the month's plan history.** A paid account warns
   only above the compute allowance its month's recorded plans grant,
   computed by the same function the pusher uses
   (`state.MonthComputeIncludedMBSeconds`), so the warning follows any later
   change to that rule. The history is read only once usage reaches the
   current plan's allowance. Free keeps its own allowance after a downgrade:
   its hard stop is what the financial model prices.
4. **Dunning reports per-account failures.** Failed suspension, deletion and
   follow-up steps fail the tick, joined, after the sweep has visited every
   account. A status that moved under the sweep and a deletion guard are not
   failures.
5. **A minute is rolled 15 s after it closes** (`api.MeterRollSettle`, set by
   meterd's loop through `Sampler.WithRollSettle`).
6. **Catch-up reaches back 72 h** (`api.MeterCatchUpWindow`), past a weekend:
   residency comes from the instance billing ledger, and instances are kept
   30 days. 15 minutes a tick drain 72 hours in under five. The instance
   listing that drains ended instances' telemetry keeps its 24 h reach as
   `api.MeterTelemetryDrainWindow`, so a live roll reads no more than before.
   `api.MeterUsageFinalAfter` becomes the window plus 3 minutes, covering the
   newest-minute offset, one tick and the settle while staying
   minute-aligned. meterd's billing lookback is never shorter than the bound
   plus a day.
7. **Price capture keeps gating completeness.** Recording a minute complete
   without an account's price would leave that account's cost evidence
   unpriced (ADR-566). The plan CHECK constraint rules out the bad-row case,
   so only a persistent database error can hold it, and
   `FaasMeterdSampleFailing` and `FaasMeterdUsageDeliveryStalled` report it
   within 30 minutes.
8. **A caught-up floor uses the minute.** With exact residency an instance
   contributes its deployment's floor when it was resident in the minute. A
   failed deployment read fails the roll; a missing deployment contributes no
   floor.

## Consequences

- Each minute is billed 15 s later; healthy `meterd_usage_final_lag_seconds`
  reads 75-135 s.
- A minute that stays unrecordable now delays provider delivery for up to
  72 h instead of 24 h; the alerts fire after 30 minutes. A new environment's
  first catch-up takes about five hours, during which
  `FaasMeterdUsageDeliveryStalled` fires.
- A catch-up tick reads up to 72 h of completeness rows instead of 24 h.
- A company app's floor during catch-up still uses its account's current
  suspension state: account state has no history to read.
- A downgraded Free account is still stopped at Free's allowance; the
  dashboard event for that stop is `billing_past_due`.

## Rejected alternatives

- **Keep the Free allowance at the month's largest grant.** It would let a
  cancelled account run free past what the Free plan prices.
- **Record complete minutes without a failing account's price.** Delivery
  would continue, but the account's cost evidence would be silently
  unpriced.
- **Shift meterd's sample tick to 15 s past the minute instead of a settle.**
  The tick starts with the process; the settle bounds every tick, including
  catch-up after a restart.
