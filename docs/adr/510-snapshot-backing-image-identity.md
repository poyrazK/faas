# ADR-510 · Restore snapshots only onto the images they were captured with

- **Status:** proposed
- **Date:** 2026-10-04
- **Amends:** spec §4.4 (snapshot restore). Extends the Firecracker version
  pinning rule to the kernel and the shared read-only base; ADR-005's
  cold-boot fallback is the recovery path.
- **Decision:** vmmd records the content identity (SHA-256) of the kernel and
  drive0 base a VM booted or restored with. Each capture (park or warm
  snapshot) writes it to the capture's `…/backing` object, a sibling of
  `…/mem`. Before loading a snapshot, vmmd compares the recorded identity with
  the files the wake would attach. A missing, unreadable or different identity
  refuses the restore before any VM process starts: the wake cold-boots and
  schedd marks the snapshot stale, as it already does for every restore that
  falls back. Snapshots captured before this ADR have no identity and are
  refused once; their next park recaptures them.
- **Why:** a snapshot's RAM contains the guest kernel's page cache and ext4
  metadata (inodes, extent maps) for drive0. The shared base is resolved
  through a logical key (`base/runner-<runtime>-amd64.ext4`) that
  `Manager.ensureBaseGeneration` and the rollout pre-stage replace in place,
  and snapshot rows recorded only a protocol stamp (`base_image_version=v1`,
  compared only for HTTP/2 and gRPC apps). On production-us, deployment
  75e8df58 had a single init snapshot captured 2026-09-30 20:54; fsn-2's cached
  runner-node22 base was replaced on 2026-10-02 at 00:06 during the rc.231
  rollout; the 18:19 restore that day loaded the old RAM onto the new base, and
  the restored Node function logged `# Check failed: !is_iterable()` 22.8 s
  later. Rebuilding the same file tree into a new image (byte-identical
  `node`, different block offsets) and restoring an init snapshot onto it
  killed every restored and respawned interpreter after at most one
  invocation in three diagnostic rounds, while the same-base control served
  960/960. With this ADR the same fixture cold-boots and serves 960/960.
- **Consequences:**
  - Every base or kernel change retires the snapshots taken on the previous
    images: each app's first wake after such a release cold-boots. Snapshots
    are cache (ADR-005), so this trades a cold start for correctness. Today
    that is almost every release, because drive0 bundles guest-init.
  - Identity is content, not path or inode, so a node restoring another
    node's capture succeeds when both hold the same image bytes and is
    refused when a node built its own, differently laid out, copy.
  - Digests are memoized per (device, inode, size). mtime is excluded because
    the cache's LRU touch rewrites it on every read; replacements always
    rename a new file, so they always change the inode. vmmd hashes the
    kernel and cached bases in the background at start-up and right after a
    base refresh, so a restore normally pays only a stat and a small object
    read. In the nested-KVM diagnostic, the first restore through a fresh
    Manager took 3.5 s unprimed and 2.5 s primed, against 2.1–2.6 s before
    this change.
  - The private writable drive stays bound by `state.SnapshotDriveKey`;
    per-deployment sidecar images use immutable keys and are not covered.
  - Managers without a storage backend (unit tests that fake the VMM) keep
    the old behaviour; production vmmd always wires storage.
  - Refusals classify as `snapshot_stale` on the wake-failure counter, so no
    new metric label is introduced.
- **Rejected alternatives:**
  - Trust the cache's `.digest` sidecar instead of hashing: production nodes
    have held cached bases whose bytes differ from the published object, so
    only the attached file's own bytes are evidence.
  - Key bases by generation (`base/<digest>.ext4`): the right long-term
    shape, but it changes publication, prestage and cache eviction across
    imaged and vmmd; the identity check is needed regardless to cover nodes
    that rebuild images locally.
  - Allow legacy snapshots until the base changes, using file birth time: a
    heuristic that cannot verify cross-node restores.
