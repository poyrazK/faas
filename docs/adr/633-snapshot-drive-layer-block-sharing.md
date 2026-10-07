# ADR-633 · Snapshot drives share their app layer's blocks, and the cache counts shared blocks once

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** ADR-063 (snapshot de-localization and the node cache budget);
  ADR-159 (immutable captures). Spec §4.6's two-drive rootfs is unchanged:
  drive1 is still a private writable copy of the app layer.
- **Decision:**
  1. When `LocalCacheBackend.Put` receives a snapshot memory, snapshot
     drive or app-layer artifact as an unread regular file, it fills its
     spool with a copy-on-write clone (`FICLONE`) instead of copying bytes.
     A captured drive is itself a clone of the instance's writable drive,
     which is a clone of the cached app layer, so the cache entry shares
     every block the guest did not write.
  2. A replica node fetches the drive whole from the parent. After the
     replica's drive and its main app layer are both local, snapshothipd
     shares every drive block whose bytes equal the layer's block at the
     same offset (`FIDEDUPERANGE`). It tries 1 MiB ranges and splits a
     differing range down to single 4 KiB blocks. The kernel compares
     bytes before sharing, so a reader of either file sees the same bytes.
  3. The cache budget counts each physical extent held by cache entries
     once (`FS_IOC_FIEMAP`): unshared extents belong to their file, and
     shared extents are merged across entries before summing. Eviction
     re-measures after each removal, so evicting a layer that its drives
     still reference does not count as freeing space. A file whose extents
     cannot be read keeps its allocated size, the previous accounting.
  4. A snapshot's `stored_bytes` counts its drive as the blocks the guest
     wrote (the instance drive's unshared bytes measured before the freeze),
     because the shared blocks are accounted under the app layer.
- **Why:** on production-us (compute-2, 2026-10-07), the 23 cached
  snapshots used about 615 MB of disk each:
  - about 240 MB of sparse memory (1 GiB guests);
  - about 375 MB of private drive (129–516 MB, 0 bytes shared).

  The instances' live writable clones shared 179 MB with their layer and
  had written only 5–31 MB. Publishing streamed the frozen clone into the
  cache, so every snapshot held its own full physical copy of its app
  layer.

  The node cache budget (`FAAS_STORAGE_CACHE_MAX_BYTES`, 24 GiB there)
  summed per-file allocation, so sharing alone would have saved disk
  without keeping one more snapshot. The budget was the binding limit:
  compute-2 evicted 57 snapshot replicas in 12 hours. An evicted snapshot
  restores only after an 8–10 s parent fetch.
- **Consequences:**
  - A drive costs the guest's writes (single-digit to tens of MiB) instead
    of a full layer copy. On the measured fleet that takes a snapshot from
    ~615 MB to ~260 MB, so a node's cache holds about 2.4× more snapshots.
  - A capture does no data copy for its drive. A replica reads its drive
    and layer once more to compare them, off the wake path.
  - Each budget check that finds the per-file sum over budget reads every
    entry's extent map; that is metadata only. Under budget by the
    per-file sum, no extent map is read.
  - A drive materialized on the wake path (a cache miss with no replica)
    still lands as a full copy until it is evicted. A periodic sharing pass
    is a follow-up if that path turns out to be common.
  - The parent still stores each drive whole (zstd), about 33–130 MB per
    capture. A delta against the layer would shrink the parent and the
    fetch; it changes the artifact format and is not part of this ADR.
  - Filesystems without reflink (ext4 hosts, local development) fall back
    to the copy, the allocated-size budget and the allocated-size
    `stored_bytes`. Nothing changes for them.
- **Rejected alternatives:**
  - Store the drive as a block delta against its app layer: this saves
    parent bytes too, but it changes the snapshot artifact format, its
    readers, and ADR-510's restore inputs. Sharing gets the node-disk win
    with no format change.
  - Count only each file's unshared extents in the budget: this undercounts.
    A layer whose every block is shared with a drive would count as zero,
    so the budget could be exceeded by the size of every shared layer.
  - Raise the cache budget instead: the disk is shared with app layers,
    bases and instance drives, and duplicated layer copies would fill
    whatever budget is set.
