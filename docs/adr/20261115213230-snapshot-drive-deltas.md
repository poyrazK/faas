# ADR-20261115213230 · Snapshot drives stored as deltas against their app layer

- **Status:** accepted
- **Date:** 2026-11-15
- **Identifier:** Renumbered from ADR-20261115120000 to distinguish this decision from full-release convergence; the snapshot-drive decision is unchanged.
- **Amends:** ADR-633 (snapshot drive block sharing), ADR-1135 (extent encoding)
- **Context:**
  - A capture publishes the instance's private drive under `snap/…/v2/drive`. The drive is a copy-on-write clone of the deployment's app layer (`layers/<deployment>.ext4`) in which the guest changed a few blocks. ADR-633 keeps it shared with the layer on the node, but the parent store received the whole drive: the layer's files again, for every capture.
  - The 2026-11-14 metal bench (`TestMetalSnapshotDedupBench`, evidence in `docs/ops/evidence/20261114-snapshot-dedup-bench/`) measured 7 blocks (28 KB) changed per park across seven Node and Python apps, against 2.6–7.1 MB compressed drive objects for those small fixture layers. Production layers are larger (about 100 MB allocated per snapshot drive on the compute nodes).
  - A node-cache miss downloads and decodes the whole drive. On 2026-11-13 the wake of a 2 GiB drive pulled from GCS took 6.2 s and returned a 503.
- **Decision:**
  1. When `FAAS_STORAGE_DRIVE_DELTA` is on, the node cache (`LocalCacheBackend`) uploads a snapshot drive handed to `Put` with its layer key (`storage.WithDriveDeltaBase`) as a delta: a `GRGDLT1` header naming the layer key, the layer's logical size and a digest of its non-zero 4 KiB blocks, then runs of changed blocks and of blocks the guest zeroed. The parent compresses the payload as before (ADR-1135). The cache's own entry stays the whole drive.
  2. Deltas are always taken against the app layer, never against an earlier snapshot drive: vmmd records each instance's layer key at cold boot and at restore, so deltas never chain.
  3. When the cache fetches a snapshot drive whose payload starts with the header, it loads the layer (from the cache, or the parent on a miss), checks its size and digest, clones it (reflink where the filesystem supports it), applies the runs and installs the whole drive. A layer that no longer matches fails the fetch; the wake falls back to a cold boot (ADR-005), which needs the same layer.
  4. If the layer is not on the node at park, the delta cannot be built, or the instance was adopted after a vmmd restart (no recorded layer), the whole drive is uploaded as before. Native exclusive publication (`PutExclusiveArtifact`) is unchanged and keeps whole drives.
- **Consequences:**
  - The parent stores and transfers only what the guest wrote for each snapshot drive. The layer is stored once per deployment, as it already was.
  - Restore on a cache hit is unchanged. On a miss the node fetches the delta (and the layer if it is not cached) and rebuilds the drive; the bench measured 3–55 ms to rebuild and cold restore p50 1.08 → 1.10 s and 1.04 → 1.30 s, warm unchanged.
  - Every reader of snapshot drives goes through the node cache, so callers, digests and receipts see whole drives. `GetCanonical` and `GetParent`, which bypass the cache, read only runtime bases and artifact scans.
  - The drive object now depends on the layer object. A deployment's layer outlives its snapshots (cold boot needs it), and the digest check refuses a layer that changed.
  - Release skew: every node decodes deltas from this release on; writing is off by default. Turn `FAAS_STORAGE_DRIVE_DELTA=1` on only after every compute node runs a vmmd with this change, because an older node that fetches a delta would install the payload as a drive. Turning it off again is safe: existing deltas stay readable.

## Amendment 1 (2026-11-27): the layer key survives cold boot and vmmd restart

- Cold boot recorded the layer key and then lost it when it made the jail, so no cold-booted guest's drive went up as a delta (fixed in #8173). Prod evidence 2026-11-27: with `FAAS_STORAGE_DRIVE_DELTA=1`, drive objects stayed 16–27 MB and `publish_drive_ms` was 7–19 s, which pushed 512 MB captures past schedd's 45 s snapshot budget.
- The adoption block can carry the key (`drive_base`, omitempty), and an adopting vmmd restores it, so an adopted guest's park uploads a delta too. Decision 4's "adopted after a vmmd restart" case then applies only to blocks without the field.
- Release skew: every vmmd from this release on reads `drive_base`. An older vmmd decodes journal records strictly, and one unknown field fails the whole journal open, so that vmmd would not start. The field is therefore written only with `adoption_drive_base` (`FAAS_VMMD_ADOPTION_DRIVE_BASE`), which is off by default. Turn it on once every compute node, and any rollback target, runs a reader. Before rolling back below that release, turn it off and let blocks be rewritten.
- A key the block cannot carry (over the adoption string limit, or a `snap/` key) is left out: that guest's park uploads its whole drive, but the guest stays adoptable.
