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

1. **Catch-up.** With the instance-billing ledger, a sample tick first rolls
   every closed minute after the newest one the sampler rolled in full, oldest
   first, then the newest closed minute. A tick that fails stops there and the
   next tick resumes from the last fully rolled minute. The app and job
   samplers keep separate progress.
2. **Bounded window.** Catch-up never reaches further back than
   `api.MeterCatchUpWindow` (30 min) before the newest closed minute. A fresh
   meterd process has no progress, so its first tick re-rolls the whole window
   once.
3. **Idempotent re-roll.** `usage_minutes` keeps the first positive
   `mb_seconds` per `(instance_id, minute)` (and synthetic floor rows use the
   deterministic ADR-060 IDs), so re-rolling a minute that is already complete
   writes nothing new; a missing or zero row is filled from the ledger.
4. **No live counters in caught-up minutes.** CPU, gateway request and
   egress deltas, and tail seconds are drained only for the newest minute.
   They accumulate at their sources while a minute is missed and land in the
   next newest minute, as before; they are not billing inputs.
5. **Metrics and coverage.** Caught-up rows are marked `CatchUp` and are not
   added to `metered_mb_seconds_total` or `meterd_floor_applied_total` (the
   failed tick may already have counted them). A minute that both the app and
   job samplers caught up in full is recorded `compute_complete` in
   `financial_sampling_windows`, whose flag only ever turns true.

## Consequences

- A meterd outage or crash loop shorter than 30 minutes no longer loses
  billable usage. Longer gaps still need an operator replay; the window keeps
  a restart on a starved host from re-walking hours of history.
- Every meterd start re-walks up to 29 minutes once (about 150 queries per
  minute on production-us today). This runs in the sample loop after
  readiness, never in the start path.
- A caught-up minute is billed from the ledger's current state, which is at
  least as accurate as the original tick: intervals closed late are included.
- The app's current status and floor policy decide a caught-up minute's
  synthetic floor; a policy change inside the 30-minute window applies to the
  minutes caught up after it.
