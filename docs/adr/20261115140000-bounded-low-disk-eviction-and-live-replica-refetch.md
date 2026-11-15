# ADR-20261115140000: Bound low-disk cache eviction; fetch back evicted live snapshots

- Status: Accepted (owner 2026-11-15: fix the preflight overshoot and re-fetch
  evicted live snapshots)
- Date: 2026-11-15
- Amends: ADR-063 (bounded node cache, evicted replicas retired),
  ADR-20261114182830 (cache budget from disk, eviction tiers)

## Context

On 2026-11-13 a retail-ui wake on gregale-compute-2 took 6.2 s instead of
about 0.4 s. Its init snapshot drive (2 GiB) and vmstate were no longer in the
node cache, so the restore pulled them from GCS, decompressed them and copied
them (1.8–4.4 s). Forty fresh snapshot replicas had been evicted in one hour.
ADR-20261114182830 fixed two causes: the budget was hand-set to 48 GiB on
100 GB disks, and eviction did not know which snapshots were live. Two causes
remain on main.

1. **Low-disk eviction overshoots.** A build preflight
   (`reclaimBuildDriveSpace`) asks the cache for 20 GiB free. In low-disk mode
   `enforceBudgetLocked` evicts the oldest entry, re-reads `statfs`, and
   repeats until the filesystem reports enough free. `/srv/fc` is XFS with
   `reflink=1`: unlinked blocks are freed by background inode GC, and blocks
   shared with a layer clone are not freed at all, so `statfs` lags. The
   preflight that needed about 1 GiB evicted about 25 GB. With the
   ADR-20261114182830 tiers this reaches live snapshots last, but it still
   reaches them.
2. **An evicted live replica is never fetched back.** Revalidation retires
   it: no longer advertised, and never re-fetched, because re-fetching on a
   full cache evicted another replica and looped (ADR-063: 1–2 GiB rewritten
   every few seconds). The next wake on that node pays the full GCS fetch.

## Decision

1. **Bounded low-disk eviction.** When the cache is within its byte budget
   and evicts only because the filesystem is below its free floor, it
   measures the shortfall once (`minFreeBytes - free`) and stops when the
   footprint it removed covers it. The footprint counts blocks shared between
   entries once (ADR-633). It also stops earlier if `statfs` already agrees.
   Over-budget eviction is unchanged.
2. **Guarded re-fetch.** When revalidation finds a replica evicted, snapshothipd
   fetches it back instead of retiring it, but only when both hold:
   - **room:** `storage.RoomToRefetch`: the cache footprint stays at least the
     margin under its budget, and the filesystem at least the margin above its
     free floor. The margin is the larger of 8 GiB
     (`SnapshotReplicaRefetchMinMargin`) and a tenth of the budget. A full
     cache, where the ADR-063 loop happened, never has room, so a re-fetch
     never evicts anything for itself.
   - **cooldown:** the snapshot was not fetched back on this node in the last
     6 h (`SnapshotReplicaRefetchCooldown`).

   Revalidation only covers non-stale snapshots of deployments in
   snapshotting, live or superseded state, so only snapshots a wake may still
   restore are fetched back. Otherwise the replica is retired as before.
3. **Metric.** `snapshothipd_replica_refetch_total{outcome}` counts
   `refetched`, `no_room` and `cooldown`.

## Consequences

- A build preflight frees what it needs, not the cache.
- A live snapshot evicted under pressure comes back within a revalidation
  cycle (5 min) once there is room, so the next wake restores from local
  disk.
- A superseded deployment's snapshots may also be fetched back. The margin
  bounds what that can cost.
- The cooldown is in memory: a vmmd restart resets it. With the room guard,
  the worst case is one extra fetch per snapshot per restart.
