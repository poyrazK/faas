# ADR 20261118003000 · The node cache keeps a whole build drive free on disks that host builds

- **Status:** proposed (2026-11-18)
- **Date:** 2026-11-18
- **Amends:** [ADR-20261114182830](20261114182830-node-cache-budget-from-disk-and-retention.md)
  (the cache's free-space floor)
- **Related:** ADR-20261027173012 (build drive space), #5709

## Context

A compute node's artifact cache shares `/srv/fc` with builder drives. Two
numbers apply to that disk:

- **The cache's free-space floor**, which it keeps by evicting: 15% of the
  disk, never below 8 GiB. On a 100 GB disk that is 16.1 GB.
- **What a build needs free:** builderd refuses a build drive with less than
  `BuildDriveMinFreeBytes` free, which is 20 GiB (21.5 GB).

Until now builderd bridged the gap itself. Before refusing a build, it asked
the cache to evict until a build drive fit (`storage.ReclaimFreeBytes`,
#5709).

ADR-20261114182830's owner-only eviction (#7539, shipped in rc.351) changed
that. Only vmmd now evicts snapshot captures and app layers, which make up
most of the cache. builderd's reclaim can no longer drop them, and vmmd
evicts only down to its own 16.1 GB floor.

On production-us, rc.351's canary build on fsn-3 waited out its 15-minute
bound and failed (2026-11-18 00:16 UTC): `insufficient host free space for
build drive: available=18335735808 bytes, required=21474836480 bytes`.
Customer source builds on such a node fail the same way.

## Decision

On a cache disk of at least `2 × api.ArtifactCacheBuildFreeBytes` (48 GiB),
the floor is at least `api.ArtifactCacheBuildFreeBytes`, which is 24 GiB:

- 20 GiB for one build drive;
- 4 GiB for a capture of the largest plan.

The vmmd eviction owner holds that floor on every cache sweep. Smaller disks
(development, CI) keep the 8 GiB / 15% floor, so they do not evict on every
write. A builderd test keeps `ArtifactCacheBuildFreeBytes` at least
`BuildDriveMinFreeBytes`.

## Consequences

- On production's 100 GB node disks, the cache's byte budget falls from
  85 GiB to 76 GiB. The first sweep after rollout evicts about 8 GB of the
  lowest-tier entries, which are captures and layers of parked apps. Their
  next wakes refetch them from shared storage.
- A source build on a full node no longer depends on builderd evicting what
  the owner keeps.
- builderd still runs its own reclaim, for the entries it may evict (bases,
  blobs, and dependency caches on the build drive).

## Amendment 1 (2026-11-18): only the eviction owner holds the build floor

rc.352 raised the floor for every process that opens the cache. On
production-us, imaged's runtime-base pre-stage on fsn-2 then failed twice.
It ran rc.352 code next to rc.351's vmmd, with about 18 GB free against the
24 GiB floor:

- **Attempt 1:** `state: runtime inputs changed after admission`.
- **Attempt 2:** `mark generation "base/runner-builder-amd64.ext4.digest"
  without cached blob`.

A process that is not the eviction owner may not evict captures or layers.
To reach the floor it evicted the bases and sidecars it could, including the
ones it had just written.

The build floor is now `storage.OwnerMinFreeBytes`. The cache holds it only
while its process is the eviction owner (`ClaimEvictionOwnership`; vmmd on a
compute node), and returns to the standing floor on release.
`ResolveCacheBudget` and every other process keep the ADR-20261114182830
floor: 15%, never below 8 GiB. The byte budget is unchanged at 85 GiB on a
100 GB disk. vmmd evicts below that to keep 24 GiB free.
