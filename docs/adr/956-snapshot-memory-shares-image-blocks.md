# ADR-956 · Snapshot memory shares the image blocks the guest page cache copied

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
- **Amended 2026-10-12: share runs of at least 64 KiB.** Every shared range
  becomes its own extent of the memory file, and a restore reads the file
  in that layout. Measured on the n2 test node (reflink XFS on persistent
  disk, Firecracker 1.17, guest 6.18), with a Node app snapshot of 409 MB
  non-zero pages, 72 MB of which matched blocks of the image the guest read:

  | shared ranges | shared | extents | warm restore | cold restore, before → after |
  |---|---|---|---|---|
  | every matching page (the original pass) | 72 MB | 5,150 | unchanged | 1.6–2.8 s → 8.5–8.9 s |
  | runs of ≥ 16 pages | 48 MB | 605 | unchanged | 1.4–4.0 s → 2.9–8.2 s |

  Page-sized sharing made cold restores 3–5× slower to save 72 MB. The pass
  now shares only runs of at least 16 pages (`memoryShareMinRun`). A run the
  kernel refuses, because a block changed after indexing, is retried in
  16-page chunks rather than page by page. Nodes whose memory files are
  read cold often can still turn the pass off with
  `FAAS_SNAPSHOT_MEMORY_SHARING=off`.

  Sharing *between* memory files, on four Node snapshots (976 MB non-zero,
  298 MB duplicated across them), behaved the same way. Page-sized sharing
  saved 299 MB in 2,201 extents per file and roughly doubled cold restores.
  Runs of ≥ 16 pages saved 205 MB in 4 extents per file, with warm and cold
  restores within noise. That makes cross-file sharing a candidate for a
  later pass under the same rule.
- **Amended 2026-10-31: share runs between memory files.**
  - **Why.** On production on 2026-10-31, in-use snapshots averaged
    153 MB of `stored_bytes`. A capture's non-zero memory breaks down as:
    - guest page cache, which this ADR already shares with the image;
    - app heap;
    - kernel data and slab;
    - the guest's struct page array, about 16 MB per GiB of plan RAM.

    The last two repeat across snapshots, and so do runtime heaps.

    A read-only replay of this pass ran over the 27 in-use memory files
    cached on compute-2 (3.3 GB non-zero, 564 MB already shared with
    images). Runs of at least 16 pages found in an earlier memory file
    covered another 659 MB, 19.8% of the non-zero memory and about 24 MB
    per file, in about 44 runs per file. Restricting matches to the same
    owner found 632 MB, only because production had two owners. Restricting
    them to the same app found 191 MB (5.7%).
  - **Decision.**
    1. **Second pass.** After the image pass, snapshothipd runs a peer
       pass. It shares the memory file's remaining runs of at least
       `memoryShareMinRun` with memory files already processed on the
       node, of any app or owner (owner's decision, 2026-10-31).
       - Pages already in a shared extent (FIEMAP) are left as they are,
         so pages shared with an image stay shared with it.
       - A run is shared in FIDEDUPERANGE calls of up to 1 MiB, with the
         same retry rule as above.
    2. **Peer index.** The node keeps an in-process index of the
       processed memory files. It holds the hash of each non-zero page at
       a 64 KiB-aligned offset of each file.
       - Any 16 consecutive pages of a file contain exactly one such
         anchor, so the anchors find every run the pass may share. The
         index is 1/16 the size of a page index: about 2,400 entries, or
         ~100 KB, for a file with 150 MB of non-zero memory.
       - A hit is extended page by page in both directions, by hashing
         the anchor file's pages a 1 MiB chunk at a time.
       - Files are recorded by inode and checked when reopened. Evicted
         or replaced files are pruned every 10 minutes.
    3. **Marker levels.** The marker records how far a file got: `1` for
       images only (every file processed before this amendment), `2` for
       images and peers.
       - After a restart, revalidation re-offers every cached snapshot.
         A level-2 file is only re-indexed, reading 4 KiB of every 64 KiB.
         A level-1 file gets the peer pass.
       - So existing caches converge within one revalidation cycle,
         without a full re-read.
    4. **Kill switch.** The pass is on by default.
       `FAAS_SNAPSHOT_MEMORY_PEER_SHARING=off` turns off only the peer
       pass; `FAAS_SNAPSHOT_MEMORY_SHARING=off` still turns off both.
  - **Why sharing across owners is safe.**
    - FIDEDUPERANGE compares the bytes in the kernel before it shares,
      and the shared blocks are copy-on-write. Every memory file keeps
      reading exactly its own bytes.
    - XFS keeps page cache per inode, not per block, so a restore of one
      file neither warms nor evicts another tenant's pages.
    - A match needs 16 identical, consecutive, page-aligned pages, and a
      guest cannot see whether any of its pages were shared. The remaining
      exposure is the general dedup timing channel, accepted here.
  - **Consequences.**
    - Node-local memory files shrink by about 20% more.
    - `stored_bytes` stays the unshared upper bound.
    - The capture log line gains `peer_shared_bytes`.
    - vmmd holds the peer index: about 100 KB per cached memory file,
      ~15 MB for 150 files.
    - Each pass reads the memory file once more, plus 1 MiB chunks of the
      peer files its runs come from, off the wake path.
    - The first memory file of a run of identical pages keeps the blocks.
      When the cache evicts it, its anchors leave the index, and the next
      file holding those pages takes their place.
    - A node whose memory files are reflink clones of a local parent copy
      sees every page as already shared and shares nothing with peers.
      Production's parent is remote.
