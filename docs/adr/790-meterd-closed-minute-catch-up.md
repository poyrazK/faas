# ADR-790 — meterd catches up closed minutes it did not roll

- **Status:** proposed
- **Date:** 2026-10-08
- **Milestone:** production hunt #6 (H5-55)
- **Amends:** spec §4.7 metering cadence; ADR-060 (floor rows); ADR-566 (financial sampling windows)

## Context

meterd's sampler rolls usage from the instance-billing ledger one closed
minute at a time: a tick at `T` rolls `[T-1m, T)` and nothing else. A minute
was therefore billed only if a tick that started in the following minute
completed its walk.

On production-us the rc.246 rollout starved the control plane's CPU (H5-52).
meterd crash-looped five times between 02:53 and 03:00; every start's sample
tick ran about 90 s and was cancelled by the next shutdown part-way through
the app walk. Minutes 02:52–03:00 kept whatever rows the cancelled walks had
written: 0–33 % of the ledger's residency per minute, and hour 02:00 was
billed 10.6 % below the ledger. Nothing ever re-rolled those minutes.
Outside that window the metered real-instance usage matches the ledger
within +0.11–0.33 % per hour.

## Decision

1. **Durable record of complete minutes.** meterd already writes one
   `financial_sampling_windows` row per closed minute whose sample tick
   succeeded (`compute_complete`). That record is the catch-up watermark:
   `FinancialCompletedComputeMinutes` lists the complete minutes of a window.
2. **Catch-up.** With the instance-billing ledger, a sample tick first rolls
   the closed minutes of the last `api.MeterCatchUpWindow` (24 h) that are not
   recorded complete and that this process has not rolled, oldest first and
   at most `api.MeterCatchUpMinutesPerTick` (15) per tick, then the newest
   closed minute. A tick that fails stops there; the next tick resumes. The
   app and job samplers keep separate in-process progress.
3. **Never twice per process.** A minute this process rolled in full is not
   re-rolled even if writing its record failed, so a persistent record or
   pricing failure cannot turn every tick into a 15-minute replay.
4. **Idempotent re-roll.** `usage_minutes` keeps the first positive
   `mb_seconds` per `(instance_id, minute)` (and synthetic floor rows use the
   deterministic ADR-060 IDs), so re-rolling a minute that is already complete
   writes nothing new; a missing or zero row is filled from the ledger.
5. **No live counters in caught-up minutes.** CPU, gateway request and
   egress deltas, and tail seconds are drained only for the newest minute.
   They accumulate at their sources while a minute is missed and land in the
   next newest minute, as before; they are not billing inputs.
6. **Metrics and coverage.** Caught-up rows are marked `CatchUp` and are not
   added to `metered_mb_seconds_total` or `meterd_floor_applied_total` (the
   failed tick may already have counted them). A minute that both the app and
   job samplers caught up in full is recorded `compute_complete`, whose flag
   only ever turns true.

## Consequences

- A meterd outage, crash loop or skipped tick within the last 24 hours no
  longer loses billable usage. On production-us the first tick after this
  ships recovers 02:52–03:00 of 2026-10-08 if it runs before 02:52 the next
  day; older gaps (2026-10-04, before deleted-app billing in #4194) need an
  operator replay.
- A restart re-rolls only minutes the record lacks; on production-us every
  other minute is recorded, so steady-state cost is one indexed range read
  of at most 1,440 rows per tick.
- A new environment with no record catches up its window at 15 minutes per
  tick; the work runs in the sample loop after readiness, never in the start
  path.
- A caught-up minute is billed from the ledger's current state, which is at
  least as accurate as the original tick: intervals closed late are included.
  A row written partially but positive by an interrupted tick keeps its first
  value; production-us showed only missing or zero rows.
- The app's current status and floor policy decide a caught-up minute's
  synthetic floor; a policy change inside the window applies to minutes
  caught up after it.
