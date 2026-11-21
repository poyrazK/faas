# ADR 20261121141532 · Bill only capacity the platform delivers

- **Status:** accepted
- **Date:** 2026-11-21
- **Amends:** ADR-060 (min-instances floor billing), ADR-137 (dead-node
  reconciler), #2611 (recovery-managed app rows), ADR-977/ADR-1137
  (service wake-ahead, declared targets)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

An audit of the owner's account on 2026-11-21 (2,796 of 3,000 GB-hours used
by the 21st, roughly triple the usual daily rate from the 15th) found GB-hours
billed for capacity the customer never received:

1. **Ghost instances on a dead node.** From 2026-11-19 17:32 every app's
   health went "unknown" for about 24 h. Instance rows on the dead nodes stayed
   `running`: the dead-node sweep leaves app rows on a draining, force-draining,
   unavailable or recovering node to the recovery arbiter, which can only retry
   a live migration from an unreachable source. The billing interval closes
   only when the row moves, so one 1 GB app with max_instances 1 was billed
   106.6 GB-h in a day with 3 requests.
2. **Unheld min-instances floors.** The synthetic floor fill bills the gap
   whenever the app's deployment is not terminal. voting-redis
   (min_instances 1) was billed its full floor through the outage, through an
   hour of 92 hostless failed boots and while parked by the platform.
3. **Platform work billed.** `snapshotting` (the terminal capture of a park,
   after the idle timeout, no longer routable) and `migrating` (a platform
   move between nodes) opened billing intervals.
4. **Wake-ahead that never learns.** A caller below 20 measured cold wakes
   wakes its first three declared `depends_on` targets on every cold wake.
   The learner is per gatewayd and in memory, and a pooled gRPC/TCP session is
   observed only when it opens, so a gRPC caller (the Online Boutique
   frontend) never reached 20 and woke checkoutservice 20 times a day with 0
   uses, each billed to its idle timeout.
5. **CLI units.** `gregale billing usage` printed per-app GB-hours with
   1 GB = 1000 MB while billing, the summary and `app costs` use 1024, so the
   rows were 2.4% higher than the bill.

## Decision

1. `InstanceBillingSeconds` bills an instance whose node's last heartbeat is
   older than `api.DeadNodeReconcilerStalenessSeconds` (120 s) when the minute
   is rolled only up to that heartbeat. A minute clamped this way stays
   unbilled even if the node later returns (first write wins), which favours
   the customer.
2. The dead-node sweep fails an app row on a recovery-managed node once the
   node has been silent for `api.DeadNodeRecoveryHandoffMaxSeconds` (30 min).
   Before that the recovery arbiter still owns the row, as #2611 decided.
3. A floor gap is billed only while the platform holds the floor. That means
   one of these is true:
   - some instance of the app was billably resident within
     `api.MinInstancesFloorUnheldGrace` (5 min);
   - the floor's deployment was created within the grace;
   - a scheduled window raised the floor within the grace.
   
   Short gaps (restarts, rollovers, crashes the customer's code causes)
   stay billed. ADR-060's billed-from-t=0 promise is kept.
4. `snapshotting` and `migrating` no longer open billing intervals
   (migration 20261121135135716) and are not `State.Billable()`.
5. Wake-ahead stops waking a declared target after
   `api.ServiceWakeAheadDeclaredMaxMisses` (3) consecutive unused or failed
   speculative wakes. It resumes once a call from the caller to the target is
   observed. Measured predictions are unchanged.
6. Every CLI surface that prints GB-hours uses `api.GBHoursFromMBSeconds`
   (1024 MB per GB).

Canary route probes are not changed. They write no usage, and since #7671
they target only in-flight rollouts, so a canary's capacity is what the
customer's rollout asked for. The aborted-canary probe traffic seen in the
audit is fixed by #7665 and #7671.

## Consequences

- Platform outages, failed placements and platform parks and migrations no
  longer show up as customer GB-hours. Revenue lost in a heartbeat stall of
  more than 120 s while the node is actually alive is accepted.
- A node silent for 30 min loses its app rows to the sweep even if recovery
  would have succeeded later; the service reconciler admits replacements.
- Wake-ahead may skip a declared target that the caller uses only through a
  long-lived pooled session until such a call is observed; the target then
  cold-wakes on first use, as it would with wake-ahead off.
