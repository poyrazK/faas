# ADR-20261118160000: Keep app ownership balanced, and keep each owner's snapshots cached

- **Status:** proposed
- **Date:** 2026-11-18
- **Amends:** ADR-20261026170000 (a new app's owner is the least owned node),
  ADR-20261114182830 (node cache retention tiers), ADR-20261115140000 (fetching
  back evicted replicas)
- **Amended by:** ADR-20261121203000 (drained apps return to their home
  node first; a drain move no longer resets the 24 h idle guard)
- **Related:** ADR-20261110210000 (owner authority), ADR-20261028200212
  (parked-only owner moves), ADR-063 (the fetch/evict loop),
  ADR-20261024225612 (CPU-compatible restore)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

**Ownership drifts and never comes back.** A new app's owner is the admitting
node that owns the least app RAM for its ceiling. Ownership moves only off a
dead or drained node (ownership recovery) or off a node under pressure.
Nothing moves apps back. On production-us, each drained release moved one
node's apps onto the other:

| When | fsn-2 | fsn-3 | Fix |
|---|---|---|---|
| rc.348, 2026-11-17 | 8 apps / 7.4 GB | 73 / 48 GB | 26 apps moved by hand |
| rc.353, 2026-11-18 03:00Z | 77 / 50 GB | 0 | 37 apps moved by hand |
| rc.355, 2026-11-18 15:40Z | 77 / 50 GB | 1 / 1 GB | 37 apps moved by hand |

Each time, every new app went to the emptier node. Release acceptance, which
needs an acceptance deployment on every active node, failed until an operator
moved apps by SQL. The skew also concentrated wakes on one node.
ADR-20261118060000 stopped releases from draining, which removes the most
common cause but not the others (a dead node, pressure moves, deletes, a new
node).

**An owner does not keep its own apps' snapshots.** Every live snapshot fans
out to every node (issue #1054), and all live snapshots share one eviction
tier. Each node's cache budget is 15% of its disk (about 16 GB on
production-us), too small for every snapshot on both nodes. On 2026-11-18:

- 76 of 142 replicas were failed with "replica evicted from the local cache".
  An evicted replica is retired for good (ADR-063) unless there is room to
  fetch it back (ADR-20261115140000).
- fsn-2 held a ready copy of only 19 of the 30 live snapshots of apps it owns;
  fsn-3 held 13 of 23.

A wake of such an app on its owner fetches the whole snapshot from shared
storage first: seconds instead of about 300 ms (a 6.2 s wake and a 503 on
2026-11-13). The same gap would make every app moved by a balance pay that
fetch on its next wake.

All 72 live snapshots on production-us are stored in shared storage, and both
nodes report the same (empty) CPU identity, so moving an app's ownership never
turns its next wake into a cold boot. It only decides where the snapshot must
be cached.

## Decision

### 1. An owner keeps its own apps' snapshots last, and fetches back what it lacks

- **Eviction tier.** A new tier, `storage.EvictOwned`, is evicted after every
  other tier. It holds the live snapshot captures and layers of the apps the
  node owns (deployments `live` or `snapshotting`). Another node's live
  snapshot keeps `EvictLast`, so the cache gives up copies of other nodes'
  apps before its own.
- **Fetching back.** On each cache sweep (`api.NodeCacheSweepInterval`, 10
  minutes), vmmd's fan-out queues every live snapshot of an app the node owns
  that it does not hold. That covers a snapshot it was never sent, and one
  that was retired by a permanent failure (an eviction or a missing object)
  more than `api.OwnedSnapshotRefetchAfter` (1 h) ago. A transient failure
  keeps its own backoff.
- **No fetch/evict loop.** The hour-long wait per snapshot bounds the churn
  that ADR-063 saw: there, a full cache re-fetched 1–2 GiB files every few
  seconds. Because owned snapshots are evicted last, a re-fetch can only push
  out other nodes' copies, unless the apps a node owns outgrow its whole
  cache, and then each snapshot is fetched at most once an hour.

### 2. The control plane keeps ownership balanced

`OwnerBalancer` runs in the control plane's schedd (role `control-plane`)
under either owner authority. Every move is the same compare-and-set as the
pressure rebalancer's (`ReassignParkedAppOwner`). Every
`api.OwnerBalanceInterval` (1 min) it:

1. Computes each admitting node's owned app RAM as a share of its admission
   ceiling.
2. Starts once the most and least owned nodes differ by more than
   `api.OwnerBalanceTriggerSpreadPct` (20 points, about 9.5 GB on a 47.6 GB
   ceiling), and keeps going until they are within
   `api.OwnerBalanceTargetSpreadPct` (10). Ordinary drift from new apps
   therefore causes no churn.
3. Moves at most `api.OwnerBalanceMovesPerTick` (5) apps, coldest first. Only
   apps with no resident instance that were neither woken nor moved for
   `api.OwnerBalanceMinIdle` (24 h) are eligible, so a busy app keeps its warm
   cache. A drain-sized skew (about 25 GB, 37 apps) is undone in about 8
   minutes.
4. Moves an app with a live deployment only if its live snapshot is stored in
   shared storage and the destination can restore it (the wake path's CPU
   rule). An app with nothing to wake moves freely. A move that would leave
   the destination owning a larger share than the source is refused, so apps
   do not ping-pong.
5. Notifies routers (`app_changed`, kind `rebalanced`, as ownership recovery
   does), then immediately queues the moved snapshots onto the new owner (§1),
   so they are local before the app's next wake.

`FAAS_OWNER_BALANCE=off` turns the balance off.

## Consequences

- Release acceptance no longer needs a hand rebalance, and wakes spread across
  nodes in proportion to their ceilings.
- An owner wakes its own apps from local disk whenever its cache can hold
  them. A moved app's snapshot is fetched in the background before its next
  wake, not during it.
- A node fetches more after a move or an eviction: one snapshot per moved app,
  and at most one re-fetch per owned snapshot per hour.
- If the apps a node owns outgrow its cache, owned snapshots still evict each
  other. The balance spreads ownership but cannot add disk; the cache budget
  is a separate decision.
