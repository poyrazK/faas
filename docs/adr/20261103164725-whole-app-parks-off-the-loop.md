# ADR 20261103164725 · Whole-app parks and deleted-app cleanup off the loop, a tick's spend caps in one read

- **Status:** proposed.
- **Date:** 2026-11-03
- **Amends:** ADR-191 (the bounded off-loop work pool), ADR-1162 (parking at
  the spend cap), ADR-098 (app delete unwinds in-flight wakes)
- **Related:** ADR 20261103131546 (scaling triggers and retention off the
  loop)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

An eighth review of `pkg/sched` found:

1. **Park, suspend and delete notifications did VM work on the select
   loop.** An `app_changed` notification of kind `parked` or
   `account_suspended` called `ParkApp`, which snapshots or destroys every
   live instance of the app, and `app_delete` called `ReconcileDeletedApp`,
   which destroys each live instance with a 5 s budget. Suspending a large
   account or deleting many previews held the stats tick, cron and the
   triggers for the sum.
2. **The reaper parked whole apps before it relieved memory pressure.** Each
   tick first parked, one after another, every app that must park (evicted,
   suspended or abuse-held account, spend cap), then ran idle parks, frozen
   expiry and measured-memory eviction. The reaper runs one tick at a time,
   so an organization with many apps reaching its cap held the eviction that
   keeps a node under its memory ceiling behind all of those parks.
3. **The reaper re-read every account's spend cap every tick.** Its check
   goes through the overage cache, whose entries last 5 s, so every 10 s
   tick read each account's cap and, for capped accounts, the month's total,
   one account at a time.

## Decision

1. **Whole-app parks and deleted-app cleanup run on the work pool.** New
   kinds `app_park` and `app_delete`, keyed by app (a duplicate coalesces),
   four apps at once, dropping on overflow. The notification still unwinds
   the deleted app's in-flight wakes at once. A dropped park is retried by
   the next reaper tick, a dropped cleanup by the reaper's lifecycle
   reconciliation.
2. **The reaper submits whole-app parks instead of running them.** It goes
   on to idle parks, frozen expiry and eviction in the same tick. `ParkApp`
   checks again under the app lock that the app must park.
3. **The reaper reads a tick's spend caps in one query** (`AccountOverageCaps`
   over the tick's billing subjects) and accepts a status up to 30 s old
   (`CheckWithin`). An uncapped subject needs nothing more; a capped one
   reads its month total at most every 30 s for the reaper. The wake gate
   keeps the cache's 5 s, and `ParkApp`'s own check uses it too.

## Consequences

- The select loop and the reaper no longer wait on whole-app parks or
  deleted-app VM destruction.
- A park the reaper decides from a status up to 30 s old is checked again
  with a fresh status before any instance is touched; reaching a cap is seen
  by the reaper up to 30 s later than before, which meterd's minute
  pricing already bounds.
- Parks of one account's apps can now run four at once.
