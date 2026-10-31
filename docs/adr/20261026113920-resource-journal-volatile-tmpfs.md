# ADR-20261026113920 · Resource journal: volatile commits on tmpfs

- **Status:** Proposed
- **Date:** 2026-10-26
- **Amends:** [ADR-20261024135625](20261024135625-resource-journal-volatile-commits.md)
  (the volatile write class) and the ADR-473 write protocol it amended. Durable
  writes keep the ADR-473 protocol unchanged.
- **Switch:** vmmd `resource_journal_volatile_writes` /
  `FAAS_VMMD_RESOURCE_JOURNAL_VOLATILE_WRITES`, default off. The tmpfs
  directory is `resource_journal_volatile_dir` /
  `FAAS_VMMD_RESOURCE_JOURNAL_VOLATILE_DIR`, default `/run/faas/vmmd-resources`.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-20261024135625 split journal writes into two classes:
- **Durable:** writes that name an asset which outlives a reboot.
- **Volatile:** writes whose assets a reboot destroys, such as mounts,
  namespaces, links and the process.

A volatile write dropped the directory fsync but kept the record-file fsync.
That fsync protects one case: after a power loss, the record name must never
point at partial content.

Production-us after that change (rc.294, which includes #5109). Data covers
fsn-2 and fsn-3, 960 restores, the 3 days up to 2026-10-26:
- `vmmd_restore_journal_commits_total`: 27 commits per restore.
- `journal_sync` phase: p50 63–65 ms, p90 79–84 ms.
- That makes `journal_sync` the second-largest restore phase, after the guest
  resume hook. It sits inside the platform's 350 ms restore budget.

About 19–21 of the 27 commits are volatile. Each still pays a record-file
fsync on the node's root ext4, about 1.6–2 ms. The durable rest are:
- the intent and checkpoint of each of the two jail directories;
- the layer clone's intent and checkpoint;
- the intent of each bind that widens a private source.

Yet a volatile version never has to survive a power loss. After a reboot, the
assets it describes are gone, and the restart inventory already treats such
records as dead (ADR-971, ADR-478). Only a vmmd restart on the same boot reads
a volatile version. For that case the page cache is enough, and so is tmpfs.

## Decision

1. **A tmpfs copy of the journal.** vmmd keeps a second record directory on
   tmpfs, default `/run/faas/vmmd-resources`.
   - Same boot: it survives vmmd restarts, because it is not a systemd
     `RuntimeDirectory`.
   - Reboot: it is empty afterwards.
   - When it holds a copy of a record, that copy is the record's newest version
     on this boot.
2. **Volatile writes (switch on).** Each volatile write replaces only the
   record's tmpfs copy: a temporary file, then an atomic rename. There is no
   fsync.
3. **Durable writes (switch on).** The tmpfs copy is replaced first. Then the
   whole record is committed to the persistent directory with the full ADR-473
   protocol: record fsync, rename, directory fsync.
   - As before, a durable write makes every earlier volatile version durable.
   - The in-memory record is published after the tmpfs rename.
   - A failed persistent commit still fails the caller closed.
4. **Removal.** The persistent file is removed first, followed by a directory
   fsync, as before. Only then is the tmpfs copy removed.
5. **Load.** vmmd reads the persistent records, then lets each tmpfs copy
   replace the record of the same name. The slot and identity checks run on
   the merged set.
6. **Boot scope.**
   - Writes require the directory to be tmpfs.
   - The directory carries a `.boot` marker holding the kernel boot id.
   - Copies under a different marker are discarded unread.
   - So after a reboot, vmmd reads exactly what ADR-20261024135625 already
     expects after a power loss: the last durable version of each record, or
     no record.
7. **Switch off.** vmmd still reads the tmpfs copies when it opens the
   journal. It then commits them to the persistent directory (record fsyncs,
   one directory fsync) and removes them. Turning the switch off on a node is
   therefore safe at any time.
8. **Rollback.** A vmmd release older than this record ignores the tmpfs
   directory. After a downgrade it would read older versions of records whose
   guests kept running. Those versions lack process, mount and link
   checkpoints. So the switch may be turned on only on nodes whose rollback
   target already contains this reader, which means this release or a later
   one. This is an expand-then-contract change across releases.

No asset moves between the classes. The record format and its validation are
unchanged.

## Consequences

- **Expected gain.** On production-us, `journal_sync` should fall from ~63 ms to
  about 20–25 ms at p50: only the ~6–8 durable commits of a restore remain.
  Concurrent wakes also wait less for the journal lock.
- **Other paths.** Parks and prepared-network replenishment stop paying disk
  fsyncs for their volatile writes too.
- **Cost.** Records are capped at 256 KiB and in practice are a few KiB each,
  one per live guest or prepared network. That is a few MB of tmpfs at most.
- **Measurement.** The restore breakdown's `journal_sync` phase and
  `vmmd_restore_journal_commits_total` measure the change. The commit count
  stays the same; the sync time per restore falls.
- **Tests.** Tests replace the tmpfs check and boot id. They pin the load
  order, discarding copies from another boot, the switch-off flush, and the
  durable-first removal order.
- **Power-loss qualification.** The journal's power-loss qualification, still
  pending since ADR-473, now has a smaller volatile surface: nothing volatile
  touches the disk.
- **Possible follow-up.** Jail directories are durable today even though
  production jails live on tmpfs. Classifying a jail directory on tmpfs as
  volatile would remove four more durable commits per restore.

## Rejected alternatives

- **Group commit per restore.** A durable intent must reach disk before its
  effect is created, so a restore's durable writes cannot wait for one shared
  fsync. Batching volatile writes cuts their number, not their purpose, and
  ADR-20261024135625 already rejected it because it widens the window in which
  a mount has no recorded identity. Volatile versions need no disk durability
  at all, so taking them off the disk is the larger cut.
- **Skip the record fsync on disk.** ADR-20261024135625 rejected this: on some
  filesystems the rename can reach disk before the data. A torn record would
  then replace the last durable version and fail vmmd startup closed.
- **Move the journal to the XFS data disk.** This still pays an fsync per
  write: 2.3 ms against 3.9 ms on root ext4, measured on production.
- **The whole journal on tmpfs.** Clones, materialised copies and widened
  bind modes must be cleaned up after a reboot, so their records need disk.
