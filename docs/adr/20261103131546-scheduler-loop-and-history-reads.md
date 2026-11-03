# ADR 20261103131546 · Scaling triggers and retention off the scheduler loop, narrow history reads, forgotten app series

- **Status:** proposed.
- **Date:** 2026-11-03
- **Amends:** ADR-191 (the bounded off-loop work pool), the instance retention
  sweep (spec §17 follow-up, PR #74, issue #2415), ADR-071 / ADR-072 (floor
  reconciler cadence), the per-app scheduler metrics (spec §12)
- **Related:** ADR 20261103094355, ADR 20261102205122 (earlier read-volume
  rounds)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A seventh review of `pkg/sched` found:

1. **The targets, floor and prewarm triggers admitted on schedd's main
   loop.** Their ticks ran inline on the select goroutine and their
   admissions wait on vmmd's restore or cold boot. While one waited, the
   loop ran nothing else: the 200 ms stats tick that feeds measured
   admission, cron, the other triggers, fire-now and operator intents.
   Scale-up had already moved off the loop.
2. **The hourly retention ran inline on every scheduler, fleet-wide, a row
   at a time.** Every wake writes an instance row kept 30 days. The sweep
   listed every expired STOPPED/FAILED row in the fleet and deleted them one
   statement each, then repeated parked batches until none were left, on
   every scheduler at once and on the main loop. Dead-letter, cron-history,
   workflow and trigger-record retention also ran inline.
3. **Two paths read an app's whole wake history.** The admission repair
   that runs when a wake would be refused at the app's cap read every
   retained row of the app, although only the rows the ledger holds can be
   repaired; the busiest apps have the longest histories and reach their
   caps most. Deleted-app cleanup read the history and reconciled each row.
4. **Per-app metric series were never deleted.** About thirty families carry
   an app label; only the egress circuit gauge was ever pruned.

## Decision

1. **The triggers run on the work pool.** Targets, floor and prewarm each get
   a one-slot kind; a tick while one runs coalesces. Admissions keep the
   engine's per-app locks and caps, as scale-up's off-loop ticks already
   did.
2. **Retention runs on the work pool and deletes in batches.** The hourly
   instance, dead-letter and cron-history retention share one kind;
   workflow and trigger-record retention get their own. Expired terminal
   rows are deleted in batches of 1,000 with `FOR UPDATE SKIP LOCKED`
   (`DeleteTerminalInstancesOlderThan`), the way parked rows already were,
   so schedulers sweeping at once split the rows. A pass stops after 200
   batches of each; the next pass continues.
3. **History reads are narrowed.** The admission repair reads the rows its
   ledger holds for the app by ID (`InstancesByIDs`); deleted-app cleanup
   reads the app's live and account-deleting rows
   (`ListAppInstancesInStates`). A store without them reads the history.
4. **Departed apps' series are deleted.** Each reaper tick compares the
   owned app list with the previous one and deletes the series labelled
   with an app that left (`OpsMetrics.ForgetApp`, under `app` or `app_id`).
   No metric is renamed.

## Consequences

- The select loop no longer waits on a boot or a retention batch.
- Targets, floor and prewarm can now run beside one another and beside
  scale-up; each admission still passes the engine's caps, and anything
  above the desired count is reaped as before.
- Retention can leave a backlog above 200,000 rows a kind for the next
  hour.
- An app that moves away and back starts its series again.
