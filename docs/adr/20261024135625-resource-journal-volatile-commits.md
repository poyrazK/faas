# ADR 20261024135625 · Resource journal: durable commits only for reboot-surviving assets

- **Status:** accepted by the owner 2026-10-24
- **Date:** 2026-10-21 (drafted as 20261021120000; renumbered on acceptance
  because that identifier is taken by the rehearsal event capture ADR)
- **Amends:** ADR-473 (write protocol), as extended by ADR-477/478/479
- **Related:** spec §6.3 wake budget, ADR-971 (dead-record reclaim),
  ADR-631 (unowned layer clones)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-473 commits every journal write the same way: private temporary file,
file fsync, atomic rename, directory fsync. A snapshot restore writes the
instance's record about 26 times while it stages the jail:
- lease or prepared-network intent;
- two jail directories;
- 4–6 bind mounts at three writes each (intent, target, mount);
- the layer clone;
- the process.

Production-us (rc.285, read-only bpftrace on vmmd, 2026-10-21) showed the cost.
One wake's chroot + stage_drives + stage_snapshot + start_jailer took 98 ms.
Inside it were ~60 back-to-back fsyncs on the node's root ext4, ~85 ms in all.
They alternate ~1.6 ms (record file) and ~1.2 ms (directory). A park adds
another 37 fsyncs (~47 ms). Each write holds the journal mutex for its fsyncs,
so background writers delay restores further, for example prepared-network
replenishment and other instances' parks.

The journal protects two kinds of event:
1. **A vmmd crash or restart on the same boot.** The page cache survives it,
   so a write that reached it is visible to the next daemon whether or not it
   was fsynced.
2. **A host crash or power loss.** Only fsynced state survives, and only
   effects that persist on disk still need cleanup after the reboot.

Most assets a restore records are gone after a reboot anyway: bind mounts and
their tmpfs placeholders, network namespaces, veth links, and the process. The
restart inventory already treats a record from an older kernel boot as dead
(ADR-971, ADR-478).

## Decision

Every journal write still fsyncs the record file before the rename, so a
record name never points at partial content after a power loss. The directory
fsync, which makes the rename itself durable, now runs only for writes that
must survive a reboot.

**Durable:** any write that adds or changes an asset whose effect outlives a
reboot (`resourceAsset.outlivesReboot`):
- layer clones and materialised snapshot copies (files on disk), including
  their identity checkpoints, which verified removal needs;
- jail directories, even though production jails live on tmpfs;
- a bind whose read permission vmmd widens on its source file. The widened
  mode stays on disk until the last owner restores it.

Every retirement and every record removal is durable, as before.

**Durable because they are off the wake path:** writes added after this record
was drafted keep the full ADR-473 protocol. That covers the adoption block
(vmmd restart adoption ADR §1, whose `op` intents also name artifacts their
effect creates on disk), the namespace re-anchor an adopting vmmd records, and
the journal epoch file.

**Volatile:** the lease and prepared-network intents, the bind target and
mount checkpoints, network namespace and veth writes, prepared-network
transfer and adoption, and the process checkpoint.

A durable write rewrites the whole record and fsyncs the directory, so it also
makes every earlier volatile version of that record durable. After a power loss,
a record may come back as an earlier complete version, or be absent if it was
created and never made durable. In both cases the missing parts name only
resources the reboot destroyed. Nothing that needs cleanup after a reboot is
ever written volatile.

## Consequences

- On production-us, a restore's ~20 volatile writes drop their directory fsync,
  about 1.2 ms each on the root disk. The expected gain is ~25 ms of restore
  staging, plus shorter journal mutex holds for concurrent wakes.
- The record format, the load rules and every reader are unchanged. Release
  N−1 reads N's records.
- Tests inject commit failures through `commitSync(durable)`, which covers both
  kinds. `TestResourceJournalCommitDurability` pins the classification.
- The restore breakdown's operator-only `journal_sync` phase (#5177) measures
  the result in production: its share of `vmmd_restore_phase_duration_seconds`
  should fall by about the saved directory fsyncs.
- Power-loss qualification of the journal, still pending since ADR-473, now
  also has to cover the volatile path.

## Rejected alternatives

- **Skip both fsyncs for volatile writes.** This would save ~95% instead of
  ~40%. But on filesystems without ext4's replace-on-rename heuristic, the
  rename can reach disk before the data. A zero-length record then fails vmmd
  startup (ADR-473 fails closed on corrupt records).
- **Move the journal to the XFS data disk** (`resource_journal_dir`). A probe on
  production measured 2.3 ms per write there against 3.9 ms on the root ext4.
  That needs no code change, but it is a production configuration change with a
  record migration. It stays open as an operator option and combines with this
  decision.
- **Batch the three writes per bind into fewer writes.** This cuts the number
  of writes, not their cost. It also widens the window in which a mount exists
  without a recorded target identity, which changes what restart cleanup can
  verify.
