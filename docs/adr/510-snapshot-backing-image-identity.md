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

## Amendment 1 (2026-10-31): node-local refusals keep the snapshot

schedd marked a snapshot stale for the whole fleet after any restore that fell
back to cold boot, so a refusal caused by one node (a storage error reading
the `…/backing` object, a base it could not fetch or hash on a full disk) and
node-local restore failures (disk full, jailer, cgroup or network namespace
setup) threw away snapshots that were still good everywhere else (bug hunt
2026-10-28 B12).

- vmmd tags those fallbacks `node_local` in the wake response's closed
  `restore_fallback_reason`. It is operator-only: schedd forwards only
  `after_restore_failed` to the customer timeline.
- schedd keeps a `node_local` snapshot live and logs the fallback. The next
  wake on any node tries it again.
- A changed identity, an absent or invalid `…/backing` object, an
  after_restore failure and every other restore failure still mark the
  snapshot stale. Refusals still count as `snapshot_stale` on the
  wake-failure counter.

## Amendment 2 (2026-11-05): the check stays off the wake path's critical time

On production-us (rc.322, 2026-11-04 18:00Z – 11-05 08:00Z) 18 of 194 wakes on
fsn-2 and fsn-3 spent 270–2,530 ms inside `bring_up` but outside `restore_ms`,
`cold_boot_ms` and `scan_check_ms`: the backing check, which no timing covered.
Two causes:

- **Hashing after a vmmd restart.** The digest memo lived only in the vmmd
  process and start-up priming covered only the configured base generations.
  Release-addressed bases (`base/releases/…`) were read in full on the first
  wake that needed them after every restart: one cron app's 426 MiB base cost
  2,315 ms, 2,419 ms and 2,530 ms on its first wake after three restarts
  (the nodes have no SHA-NI). A base the release pre-stage replaced under a
  still-serving vmmd cost 443 and 456 ms before the refusal it led to.
- **Reading the identity from shared storage.** The snapshot replica runner
  (`snapshothipd`) cached a capture's memory, vmstate, drive and layers on
  every node, but not its `…/backing` object, so a restore on a node other than
  the capturing one read it remotely (~340 ms; the cached memory showed the
  replica itself was local).

Decisions:

- **Digests persist with the file.** After hashing a file version vmmd records
  `v1 <size> sha256:<hex>` in the file's `trusted.faas.backing-digest` extended
  attribute, through the open descriptor. A later vmmd process uses it instead
  of reading the file. The attribute belongs to the inode: a replacement
  renamed into place has none, and a recycled inode number starts without one.
  Only CAP_SYS_ADMIN can read or write the trusted namespace, so vmmd (the only
  root daemon) is the only writer; imaged and the other daemons sharing the
  node cache cannot forge one. A record whose size does not match the file is
  ignored. Where the attribute cannot be written (a read-only kernel image, a
  filesystem without trusted xattrs) the in-process memo still applies.
- **The memo key gains the birth time** where the filesystem records one
  (XFS, ext4): (device, inode, size, birth time). Equal sizes are common
  (bases are sized in whole megabytes), so a recycled inode number of an
  evicted base could otherwise inherit its digest within one process.
- **vmmd keeps priming.** Every 30 s it identifies the kernel, the configured
  bases and every base key a wake on this node attached or verified (at most
  256), resolving them without advancing the cache LRU and without fetching.
  A version it already knows costs an open and a stat. Keys the cache evicted
  are dropped until a wake attaches them again.
- **Replicas carry the identity.** `snapshothipd` caches a v2 capture's
  `…/backing` object with the rest of the replica, best-effort: an absent
  object (a capture from before this ADR) or a failed read never changes the
  replica's state, and revalidation adds it to replicas made before this
  change.
- **The check is attributed.** `wake ok` logs `backing_check_ms` and its
  disjoint parts `backing_sidecar_ms`, `backing_fetch_ms`, `backing_hash_ms`.
  `wake.restore_breakdown` carries them as ADR-192 prepare timings (outside
  `total_ms`), and `wake.cold_boot_breakdown` carries them when a refused or
  failed restore preceded the cold boot. Both omit the keys when they are zero.

What the check guarantees is unchanged: every restore compares the capture's
recorded kernel, base and timer identity with the content identity of the
files it would attach, and a missing, unreadable or different identity refuses
the restore before any VM process starts. As before, a rewrite of a cached
image in place that keeps its inode and size is not detected; every writer
renames a new file into place (Consequences above). The attribute
extends that assumption from one vmmd process to the life of the file.

Rejected: trusting imaged's `.digest` sidecar (rejected in the original
decision; the cached bytes are the evidence); a vmmd-owned digest file keyed
by inode and birth time (needs pruning and a birth time on every filesystem,
which the attribute does not); verifying in parallel with the restore's
staging (hides at most the ~100 ms of staging and turns a refusal into a
half-built restore to tear down).

## Amendment 3 (2026-11-05): a release keeps the base when guest-init's code is the same

Consequences above said every base change retires the snapshots on the old
one, "almost every release, because drive0 bundles guest-init". Production
confirmed it: each release's guest-init had new bytes (26 digests in 12 days),
so each release replaced every base and each app's first wake was refused
(205 of 207 restore fallbacks on rc.322). The bytes changed with guest-init's
whole import closure, not only with its code: the Go build ID and line table
of two builds of the same code differ.

The check is unchanged and stays exact. What changes is what the release
puts in the base: it ships the previous signed release's guest-init while the
code digest of the new build is the same (ADR-20261105150000). Of five recent
release pairs one was that case; a release that changes what guest-init runs
still retires the snapshots once.
