# ADR 20261103195512 · Restore pressure without a fleet lock; failure destroys, the jobs reaper and run-now drains off the loop

- **Status:** proposed.
- **Date:** 2026-11-03
- **Amends:** ADR-1741 (the one-round-trip restore-pressure lease), ADR-191
  (the bounded off-loop work pool)
- **Related:** ADR 20261103164725 (whole-app parks off the loop), issue #3359
  (relayed instance failures), PR-D / issue #791 (cron run-now)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A ninth review of `pkg/sched` found:

1. **Every snapshot restore in the fleet passed through one Postgres
   lock.** Restore placement took the session advisory lock
   `snapshot-restore-pressure:v1`, read the per-node lease counts, chose a
   node and inserted a lease before unlocking. Waiters retried every 10 ms,
   and a wake that had not got the lock within 250 ms gave up on the shared
   counts and used its own schedd's counts. A burst of wakes (crons on the
   minute, a deploy, returning traffic) queued on the lock, adding latency to
   restores, which have a 350 ms p95 target. Past 250 ms the
   coordination turned itself off, at the moment it mattered most.
2. **Relayed instance failures destroyed VMs on the select loop.**
   `instance_failure_relayed` ran `DestroyForWorkloadOOMFailure` or
   `DestroyForLivenessFailure` inline. Each takes the app lock (held for a
   whole boot by a waking instance of the same app) and waits up to 5 s on
   vmmd.
3. **The stuck-job reaper destroyed VMs on the select loop.** Its 5 s tick
   ran up to 64 timed-out tasks and 64 orphaned job instances, each destroy
   with a 5 s budget, inline.
4. **Run-now drains ran on the select loop.** Each `cron_run_now`
   notification and the minute safety tick enqueued up to 256 manual cron
   runs, several database reads each, inline.

Items 2–4 held the stats tick, cron and the scaling triggers while they ran.

## Decision

1. **Restore pressure is read without a lock.** It is a soft placement
   signal (a watermark filter and a tie-break), not a cap, so it does not
   need a critical section. Each wake reads the unexpired leases of the
   other schedds in one query, leaving out its own schedd's lease IDs, and
   adds its schedd's in-flight restores, which the engine counts itself.
   Choosing the node and counting the local reservation stay atomic inside
   one schedd. The lease insert stays in the background (ADR-1741). The
   release deletes the lease and any expired ones. The 250 ms bound now
   covers only the count read. The old `snapshot_restore_pressure_acquire`
   function stays for release N-1 schedds, whose locked reads still count
   the new schedds' leases.
2. **Relayed failures run on the work pool** (`instance_failure`, 8 slots,
   keyed by instance and failure kind). Nothing replays a consumed relay, so
   a full pool runs the destroy inline as before rather than drop it.
3. **The stuck-job reaper runs on the work pool** (`jobs_reaper`, one pass
   at a time, a tick during a pass is dropped and the next tick finds what is
   left).
4. **Run-now drains run on the work pool** (`cron_fire_now`, one at a time).
   A notification during a drain asks for one more pass; the minute safety
   tick covers anything else. The startup drain stays inline.

## Consequences

- Restore placements of different schedds no longer wait for each other,
  and the shared counts no longer switch off under a burst.
- Two schedds placing at the same moment may both miss the other's lease
  (one round trip wide) and pick the same node. The watermark is a soft
  limit, so the cost is an extra concurrent restore on one node at worst.
  Inside one schedd nothing changes.
- A lease released locally is counted as another schedd's until its delete
  lands, overstating pressure briefly; overstating only spreads restores.
- The select loop no longer waits on relayed-failure destroys, stuck-job
  destroys or run-now enqueues.
