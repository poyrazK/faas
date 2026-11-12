# ADR-20261105100000: vmmd launches the jailer and firecracker from verified copies on the jail tmpfs

## Status
Proposed.

## Context

The first snapshot restore after a compute node has been quiet pays a stall in
`bind_tun` that later restores do not. On rc.322 (read-only, 2026-11-03 to
2026-11-05) 47 of 1,087 restores (4%) took 150 ms or more there, against a
normal ~10 ms (p50 9 ms on fsn-2). The worked example, pulsewatch-web on fsn-2 at
2026-11-04 22:17:44Z, spent 223 ms in `bind_tun` and 35 ms in `start_jailer`.

What the data supports:

- Slow restores are the first restore after the node was quiet: the median gap
  since that node's previous restore was 1,797 s on fsn-2 and 899 s on fsn-3,
  against 13–17 s for fast ones. Concurrency with other wakes does not explain
  it; fast restores overlap more.
- `start_jailer` has a median of 31 ms on slow restores against 3 ms on fast.
- `journal_sync` is ~57 ms on slow and fast restores alike, and
  `journal_lock_wait` is 0, so the resource journal is not the difference.
- `node_disk_read_bytes_total` in the 30 s around a restore is a median 13 MiB
  on slow restores (all above 1 MiB) against 1 MiB on fast ones (about half
  above 1 MiB).

The window is `tBindTun - tStartJailer`: the native path journals, waits for the
jailer to unshare its mount namespace and pivot into the jail, runs the
journaled device-setup helper, then applies the pre-boot cgroup fence. Only
`start_jailer` and `bind_tun` were exported, so nothing said which part was slow.
The sub-phases existed in `restoreTimingBreakdown` but reached a Debug log, and
the native path (what production runs) filled only one of them.

The likely cause, not proven: the jailer (2.2 MB) and firecracker (3.7 MB)
executables sit on the node's persistent disk. Every launch executes the jailer
and the jailer copies firecracker into the new chroot. The nodes have 16 GB of
RAM, about 10 GB of page cache, no swap, and heavy churn from GB-sized layer
files, so a binary nothing has mapped for half an hour can be evicted and then
read back from a busy disk on the wake path. The helper binary already avoids
this: it runs from a tmpfs copy at `/srv/fc/jail/.faas-mount-helper`. A
read-only `mincore` of both binaries on both nodes at 2026-11-05 08:58Z found
every page resident, which neither confirms nor refutes the hypothesis (the
nodes had just served wakes).

## Decision

1. **Export the sub-phases.** `vmmd_restore_phase_duration_seconds{phase}` gains
   `tun_journal_checks`, `tun_wait_mntns`, `tun_wait_chroot`, `tun_setup_jail`
   and `cgroup_fence`. Together they partition `bind_tun`; do not sum across
   the label set. On the native path `tun_journal_checks` is the ownership and
   journal checks before it starts waiting, the two waits are the jailer's
   namespace unshare and its pivot into the jail (the jailer copying firecracker
   into the chroot is inside the second), and `tun_setup_jail` is the rest: the
   journaled helper, its receipt and descriptor handoff. Existing metric names
   and the `wake.restore_breakdown` event are unchanged.

2. **Launch from verified copies on the jail tmpfs.** vmmd copies the installed
   jailer and firecracker into `<chroot-base>/.faas-resident/` at startup
   (`PrepareResidentBinaries`, best effort) and launches the jailer, and hands
   it `--exec-file`, from those copies. tmpfs pages are not reclaimable without
   swap, so they stay in memory.
   - The copy keeps the installed basename (`jailer-v1.17.0-x86_64`,
     `firecracker-v1.17.0-x86_64`). The jailer names the chroot directory after
     the firecracker basename, and native recovery recognises a launch by both
     basenames and by `--chroot-base-dir`; none of that changes.
   - The bytes are hashed as they are read from the open installed file and
     again as they sit in the copy, and the installed file must be the same
     file, size, mode and modification time before and after. Anything that is
     not a non-empty executable regular file, or that a group or others can
     write, is refused.
   - Each launch stats the installed file and the copy. If the installed file
     changed (an Ansible upgrade flipping the symlink) or the copy is not the
     file vmmd published, the copy is made again. Superseded copies are removed
     at the next vmmd start.
   - Staging is a cache of the installed binary, never a requirement (ADR-005).
     If it fails, the launch uses the installed binary exactly as before, and
     `vmmd_resident_exec_stages_total{binary,outcome="fallback"}` counts it. A
     failure is logged once per distinct cause.

## Trust model

Unchanged: vmmd already executes the installed path as root, so the installed
bytes, which Ansible pins by the release tarball's SHA-256
(`roles/firecracker`), remain the trust anchor. The copy is a faithful, verified
duplicate of what would have run. It is root-owned in a root-only directory
inside the root-only jail tmpfs, which the installed files are not: on the
production nodes they are owned by uid 14133424, the tarball's owner, because
`unarchive` preserves it. That is a deployment finding (the role should extract
as root), not changed here; staging only narrows the window in which an
unprivileged owner could matter. Firecracker version pinning is untouched:
`snapshots.fc_version` still comes from the installed binary at startup, and the
copy tracks the installed file, so the version that launches is the version
vmmd detected, as before. Ansible still owns where binaries are installed and
how they are upgraded; on compute nodes it still checks the installed
Firecracker digest against the signed release manifest at converge, and vmmd
does not repeat that check at launch, then or now. The deploy order is
unchanged. The `faas_vmm` auditd watches stay on the installed paths and keep
recording what they did; vmmd's launches already execute the resolved
version-named files, not the symlinks they name. Every VM still starts
through the jailer with its own uid, chroot, seccomp filter and cgroup, and
nothing is shared into a guest.

## Consequences

- About 6 MB of the jail tmpfs (12 GB on the nodes) holds the two copies, charged
  to vmmd's cgroup in the control-plane slice, not the tenant budget. Invariants
  2 and 4 are unaffected.
- A non-tmpfs jail base still works; the copy is then just another disk file.
- The launch adds two stats on the hot path, both of metadata the launch already
  reads (`LookPath` and symlink resolution).
- A sample from a quiet node is the evidence that decides this. After a release,
  on restores that follow a quiet spell: if `tun_wait_mntns` and `tun_wait_chroot`
  (and `start_jailer`) no longer grow, the executables were the cause. If the time
  sits in `tun_journal_checks` or `tun_setup_jail`, the journal's file reads or
  fsyncs are next, and a follow-up splits `tun_setup_jail` further.
- Not measured here: no Linux host was available to drop caches and time a jailer
  start. The prod figures above are rc.322 read-only observations; nothing in this
  change has run on a native host yet.

## Validation

Table-driven tests in `pkg/fcvm` cover staging (modes, empty, directory,
missing, group- and world-writable sources), reuse and re-staging when the
installed file or the copy changes, fallback and its metric, startup pruning,
the launch argv (resident jailer, resident `--exec-file`, same chroot name and
recognisable to native recovery), and the `bind_tun` partition including the
failed-early and clamped cases. `make test-metal` and `make leakcheck` are
pending on the native x86_64 KVM hosts: the jailer wait measurements in
`linuxNativeJailDevices.Prepare` only run there.
