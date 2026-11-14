# ADR-20261114120000: The node cache sizes itself from its disk, drops what nothing references, and evicts live snapshots last

## Status
Proposed.

## Context

Every compute node keeps snapshot captures (memory, vmstate, drive, backing),
app layers, runtime release bases and kernels in its read-through artifact
cache (ADR-054 §2). Measured read-only on production-us, 2026-11-12/13:

- **The budget was smaller than the disk.** The budget is half of node RAM,
  capped at 32 GiB (`ResolveCacheMaxBytes`); production overrode it to 48 GiB
  by hand (`FAAS_STORAGE_CACHE_MAX_BYTES`, not in the repository) on 100 GB
  cache disks. On 2026-11-13 compute-2 evicted 40 fresh snapshot replicas in
  one hour while its disk had 36 GB free; the next wake of one app fetched its
  2 GiB drive from shared storage, took 6.2 s and answered one request 503.
- **Deleted snapshots stay on every node.** imaged deletes a snapshot's row
  and its shared objects, but `LocalCacheBackend.Delete` only evicts on the
  node that calls it. compute-2 held 171 memory files of deleted snapshots
  (16.7 GB allocated, 10.2 GB of their own blocks), all younger than three
  days, with their drives beside them. The 1-hour stale retention
  (`api.SnapshotStaleRetention`, #7038) makes deletions frequent.
- **Runtime release bases are never evicted.** Each node cached 67 runtime
  release bases (24 GB on compute-1), all read within the last day, so
  oldest-first eviction never reaches them. 94 releases exist; 35 are bound
  to live deployments and 7 to superseded ones.

Eviction is already guarded by a free-space floor
(`api.ArtifactCacheMinFreeBytes`, 8 GiB): the cache evicts oldest-first while
its filesystem has less free than the floor. The budget cap existed mostly
because the eviction walk stats every entry on each write; that walk runs on
every write whatever the budget, so its cost follows the number of entries,
not the budget.

## Decision

1. **The budget comes from the disk.** With `FAAS_STORAGE_CACHE_MAX_BYTES`
   unset, the budget is the cache filesystem's size less a reserve of
   `api.ArtifactCacheReservePercent` (15%) of the disk, never less than
   `api.ArtifactCacheMinFreeBytes`, or the RAM-derived budget when that is
   larger (`storage.ResolveCacheBudget`). The reserve is also the free-space
   floor, so captures, writable layer clones and build drives keep their room
   by eviction, not by an artificially small budget. A set
   `FAAS_STORAGE_CACHE_MAX_BYTES` still caps the budget. On a 100 GB disk:
   85 GiB budget, 15 GiB floor; on 1.5 TB: about 1.3 TB and 230 GB.
2. **Each node removes what nothing references.** vmmd's snapshothipd sweep
   (every `api.NodeCacheSweepInterval`, 10 minutes) reads, in one pass, every
   capture key a row references (snapshots, environment branch captures,
   crash captures, in-flight live migrations) and every runtime release with
   whether a deployment that is not failed or cancelled is bound to it or an
   upgrade targets it. It removes from this node's cache only:
   - objects of an immutable capture (`snap/…/captures/<id>/v2/*`) that no row
     references, once cached longer than `api.SnapshotCacheOrphanGrace`
     (6 h): a capture is cached before schedd records its row;
   - runtime release bases nothing uses, once the release is older than
     `api.RuntimeReleaseCacheGrace` (24 h), and release bases the database
     does not know, once unread for as long.
   Legacy mutable snapshot keys, app layers, bases and kernels are never
   swept. A failed read removes nothing. Removal never touches shared storage:
   a later read fetches the object again (snapshots are cache, ADR-005).
3. **Eviction order follows what is live.** The same pass gives the cache an
   eviction tier per key: a stale snapshot's capture and an unused runtime
   release first, a live (non-stale) snapshot's capture last, everything else
   in between; oldest first within a tier. A capture no row references yet
   stays in the middle tier until the sweep can tell it is an orphan, so a
   fresh capture is never evicted first.

`FAAS_NODE_CACHE_SWEEP=off` turns off 2 and 3 on a node.

## Consequences

- After rollout, a node's cache grows to its disk less the reserve instead of
  48 GiB. The production nodes' `FAAS_STORAGE_CACHE_MAX_BYTES=51539607552`
  (and a 24 GiB value in another env file on compute-2) must be removed for
  1 to take effect; while set, it caps the budget. That is an operator step.
- The eviction walk covers more entries on a larger cache; at today's few
  hundred entries per node it is a directory read and a stat per entry.
- Orphaned captures leave each node within the 6-hour grace plus one sweep;
  unused runtime release bases within a day of their release.
- A runtime release a deployment binds to after its base was swept is
  fetched again on that deployment's first cold boot on the node, as on a new
  node.
- vmmd reads two small queries every 10 minutes per node.

## Rejected

- **Evict on the node when imaged deletes the row** (a notification to every
  node). A node that is down or misses the notification keeps the files
  forever; the sweep is needed anyway, and alone it is enough.
- **Pin live snapshots in the cache.** A pinned entry is never evicted, so a
  disk full of live snapshots would refuse new captures. An eviction tier
  keeps them longest without making them unevictable.
- **Keep the budget, raise it by hand per node.** It drifts from the disk the
  node has, which is how a 100 GB disk ended up evicting at 48 GiB.
