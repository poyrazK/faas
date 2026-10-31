# ADR-20261031103801 · Dependency caches share one copy of platform image layers

- **Status:** Proposed
- **Date:** 2026-10-31
- **Amends:** ADR-190 (production BuildKit dependency cache) and ADR-153
  (its 16 GiB budget), for how builderd stores published caches on a node.
  Cache keys, their per-app scope, and what a build imports are unchanged.
- **Switch:** builderd `FAAS_BUILD_SHARED_PLATFORM_BLOBS`, default off.

## Context

guest-init exports each app's dependency cache with
`--export-cache type=local,mode=max`. `mode=max` writes every layer of the
build, and that includes the Railpack builder image
(`ghcr.io/railwayapp/railpack-builder:mise-2026.8.13`) that every Railpack
plan starts from. Its gzip layers land in the cache unchanged. On both
production compute nodes on 2026-10-31:

| | compute-1 | compute-2 |
|---|---|---|
| Published caches | 28 | 29 |
| Cache bytes | 16.8 GB | 17.2 GB |
| Builder image layers among them (27 copies) | 11.3 GB | 11.3 GB |

The copies are byte-identical to the image on ghcr: blobs `59e1e910…`
(240.4 MB), `cbc19164…` (67.8 MB), `27ee9a82…` (49.3 MB), `97ddafb7…`
(34.6 MB) and `00f78834…` (25.6 MB). A Railpack cache is about 620 MB, and
two thirds of it is this image.

This costs on two budgets:

- **The 16 GiB cache budget (ADR-153).** Both nodes sit at it. The sweep
  evicts the oldest caches, so each app's 200 MB of real dependency layers
  carries 418 MB of a shared public image. About a third as many apps keep
  a cache as the budget would hold.
- **Free space on `/srv/fc`.** It was 23–25 GiB free against a
  `BuildDriveMinFreeBytes` floor of about 20 GiB. Below the floor builderd
  requeues the build and reclaims dependency caches first (ADR-20261029103512).
  On 2026-10-27, before that requeue existed, the floor failed 44% of deploys.

ADR-190 rejected a shared cross-app cache as an unnecessary tenant-isolation
risk. That still holds for cache records and for anything a tenant's build
produced.

## Decision

builderd keeps one copy of each **platform layer** on the node and
hard-links it into each published cache that contains it.

1. **Platform layers.** These are the layer blobs of public images that
   every Railpack build pulls by itself, independent of the app: today the
   Railpack builder image's amd64 layers. They are listed by digest in
   builderd, next to the Railpack version they belong to. A unit test fails
   when `RAILPACK_VERSION` changes without the list, and
   `scripts/railpack-platform-layers.sh` prints the list for an image. A
   stale list only means no sharing, never a wrong build.
2. **Store.** Shared copies live in
   `<drive dir>/dev-dependency-cache/.platform-blobs/sha256/<hex>`. They
   are mode 0444 and owned by builderd. Nothing writes to a cache file in
   place; publication, staging and sweep only create, rename, link and
   unlink.
3. **Publication.** For each blob in a new cache whose name is on the list,
   builderd hashes the file.
   - **Content matches the name:** builderd replaces the file with a link to
     the stored copy. If there's no stored copy yet, this file becomes it.
   - **Any mismatch or error:** the cache keeps its own copy.

   builderd never trusts a blob name the guest exported. A blob that isn't
   on the list is never shared, so no tenant-produced bytes are shared.
4. **Accounting.** The sweep counts each inode once across the cache root.
   It evicts a cache for the bytes only that cache holds. It removes a
   stored copy once no cache links to it (link count 1). The 16 GiB budget
   and the 48-hour TTL keep their meaning, in bytes on disk.
5. **Builds.** Staging a cache onto a build drive already links the files
   it copies into ext4 (`stageDependencyCacheForDrive`), so a build imports
   exactly the bytes it would have before. BuildKit still validates every
   record and blob it imports.

**Rejected:**
- *Content-addressed sharing of any blob two apps have in common.* It saves
  little more, because app layers aren't reproducible across apps. It would
  also share tenant-produced files between tenants, which is what ADR-190
  rejected.
- *Dropping platform layers from the exported cache.* BuildKit would refer
  to blobs the cache no longer has, and the import would fail or miss.
- *`mode=min`.* It doesn't cache the intermediate dependency steps that
  ADR-190 exists for.

## Rollout

1. Land with the switch off: the list, its version check, publication linking,
   inode-aware accounting and the sweep, with tests for:
   - a mismatched or renamed blob staying private;
   - a stored copy outliving the cache that created it;
   - eviction freeing exclusive bytes only.
2. Turn the switch on for one node's builderd. Compare its cache bytes, its
   cache count before eviction, and its cold-build rate with the other
   node's for a day.
3. Make it the default.

## Consequences

- Expected: cache bytes for today's caches on each node fall from about
  17 GB to about 6 GB. The same 16 GiB budget keeps about three times as
  many apps' caches, and `/srv/fc` gains about 11 GB of headroom over the
  build-drive floor.
- Publication hashes the shared blobs it links, about 420 MB per Railpack
  cache: roughly 1.2 s on the current CPUs, which have no SHA instructions.
  With deferred cache export (ADR-20261029171525) this runs off the
  deploy's critical path.
- Each build still copies its whole staged cache into its drive. This ADR
  saves disk, not per-build I/O. The seed drive (ADR-20261030122204) is
  what would make the builder image's bytes unnecessary inside a build.
- A Railpack version bump needs the list updated in the same change. A
  unit test enforces this.
