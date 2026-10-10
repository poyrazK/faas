# ADR-942 · Snapshot memory shares the image blocks the guest page cache copied

- **Status:** proposed
- **Date:** 2026-10-10
- **Amends:** ADR-633 (snapshot drives share their app layer's blocks). The
  snapshot artifact format, ADR-165 compression and ADR-510 restore inputs
  are unchanged.
- **Decision:**
  1. After a node holds a snapshot's memory file in its cache, snapshothipd
     shares every 4 KiB memory page whose bytes equal a 4 KiB block of the
     snapshot's cached app layer(s) or of a runtime base cached on the node
     (`FIDEDUPERANGE`, memory file as the destination). Consecutive pages
     that are consecutive image blocks are shared as one range of up to
     1 MiB; a range the kernel reports as differing is retried page by page.
  2. Candidate blocks come from an in-process hash index. The kernel
     compares the bytes before sharing, so an index collision only costs a
     refused attempt and a reader of either file sees its original bytes.
     The base index is built once per set of cached base files and reused
     for every snapshot; the app-layer index is built per pass.
  3. The pass runs on one background worker per node, fed by the replica
     queue: every node holding the snapshot gets a replica job, including
     the capturing node, and revalidation re-offers it every five minutes.
     A processed memory file carries the `user.faas.memory-shared` xattr
     (and an in-process record), so it is processed once; a cache refresh
     writes a new file without it. The queue is bounded and drops offers
     when full; revalidation re-offers them.
  4. The pass is best-effort: an evicted or replaced image is skipped, and a
     filesystem that cannot share blocks ends the pass with nothing shared.
     `FAAS_SNAPSHOT_MEMORY_SHARING=off` disables it.
- **Why:** on production (compute-1, 2026-10-10) the 12 cached snapshot
  memory files held 156–261 MB of non-zero pages each. Matching every page
  against the node's cached images:

  | content | MB per snapshot |
  |---|---|
  | app-layer file blocks | 40–131 |
  | runtime-base file blocks | 0–53 |
  | kernel | ~5 |
  | other (anonymous memory, kernel data) | 62–125 |

  55–60% of each memory file is the guest page cache: a second physical copy
  of blocks the node already stores in the app layer or base. Dropping the
  guest page cache before capture would remove those pages from the
  snapshot, but every wake would then read them back from disk, against the
  §6.3 wake budget. Sharing removes the duplicate copy and leaves the
  restored guest's memory exactly as captured.
- **Consequences:**
  - Node-local memory for the measured snapshots goes from 156–261 MB to
    roughly 65–125 MB. ADR-633's cache budget already counts shared extents
    once, so the node cache holds correspondingly more snapshots.
  - `snapshots.stored_bytes` is measured at capture, before sharing, and
    stays the snapshot's unshared upper bound.
  - Each pass reads the app layer's allocated blocks and the memory file
    once, off the wake path; the base index costs one read of each cached
    base per base version. The index holds about 24 bytes per distinct
    image block (~40 MB for the measured node).
  - Sharing invalidates the memory file's page cache on the host. A restore
    that runs concurrently reads the same bytes from the shared blocks.
  - The parent store and the fetched bytes are unchanged; zstd (ADR-165)
    already compresses the memory blob to ~50 MiB.
  - ext4 hosts and local development cannot share blocks; nothing changes
    for them.
- **Rejected alternatives:**
  - Drop the guest page cache (and zero freed pages with `init_on_free=1`)
    before capture: smallest artifact, but moves 40–180 MB of reads per
    wake onto the restore path.
  - Share pages only against blocks at the same offset, as ADR-633 does for
    drives: guest physical addresses bear no relation to file offsets, so
    almost nothing would match.
  - Run the pass inline in vmmd's capture: it would add seconds of reads to
    every park, which already publishes the snapshot synchronously.
