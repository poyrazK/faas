# ADR-20261025090000: The host kernel never mounts a drive a guest has written

Date: 2026-10-25
Status: Accepted
Amends: [ADR-192](192-wake-hot-path-staging-and-attribution.md) (staging session mechanism), [ADR-568](568-environment-gitops-contract.md) (native staging fence keeps its rules, loses its loop frames)
Related: spec §4.6 (two-drive rootfs), spec §11 (security rules), [ADR-732](732-production-forks.md) (fork scrub)

## Context

A tenant is root inside its microVM. Customer images that set no `USER` run
their processes as uid 0, and guest-init mounts a full devtmpfs, so a tenant
can open `/dev/vdb` and write any block of its writable drive: drive1 (the
per-app layer, the overlay upper) for apps and jobs, the scratch drive for
builders. Nothing on the host checks those blocks afterwards.

vmmd then mounted those images with the host kernel:

| Site | When | Image | Mount |
|---|---|---|---|
| Pre-boot staging (`stagePreBootFiles`) | every snapshot restore (wake, scale-out, warm pool, migration, sealed capture, fork) | the captured drive the guest had mounted | `loop,rw` (journal replay) |
| Fork scrub (`scrubForkSecrets`) | every ADR-732 fork | the captured production drive | `loop,rw` |
| Build export (`exportBuildArtifacts`) | every build | the builder drive after untrusted build code ran as root | `loop,ro,noload`, falling back to `loop,ro` (journal replay) |
| Native staging and qualification restore | native mode | the same drives | `unix.Mount(ext4)` over `LOOP_CONFIGURE` |

The mount options (`nodev,nosuid,noexec,errors=remount-ro`) kept guest
inodes inert and stopped a guest-chosen `errors=panic`. They do nothing about
the parser. The kernel's ext4 code (superblock, group descriptors, extent
trees, htree directories, the jbd2 journal) then runs on bytes the attacker
chose. The kernel does not treat mounting an untrusted filesystem image as a
supported security boundary, and memory-safety bugs in that code have been
found repeatedly. Any account with a build or a parked app could reach this:
a bug there is a host compromise from inside a VM, which is the boundary the
platform exists to keep.

ADR-192 had considered `debugfs` instead of the mount and rejected it as
"unsafe against a live journal". That was correct: a captured drive is
mounted in the guest, so its journal holds committed transactions, and
debugfs writes underneath them. A later replay would put stale blocks back
over the edits.

## Decision

### 1. No VM drive is ever mounted or loop-attached on the host

Every read or write of a VM drive goes through `pkg/guestdisk`, which runs
e2fsprogs in a sandboxed child. `pkg/fcvm` no longer contains a mount of
ext4, a loop mount or a loop-device attachment, and
`TestNothingInFcvmMountsADriveOnTheHost` fails any change that adds one. The
host still mounts images it built itself and that no guest ever ran on:
imaged's parent bases and the runtime-scan sources (`pkg/vmmdmount`),
verified by digest and size before use.

### 2. The sandbox

vmmd starts `vmmd-guestdisk`, a small static helper that ships beside vmmd in
the compute release unit. vmmd does not re-exec itself: its package init
costs about 88 ms, and a restore makes three tool runs. There is no
fallback. Without the helper, vmmd refuses to touch a drive rather than parse
one as root. For every tool run the helper:

- starts in fresh mount, network, IPC and UTS namespaces (cloned by vmmd), so
  the tool has no network (only a down `lo`), no abstract unix sockets and no
  SysV IPC;
- covers `/tmp`, `/var/tmp`, `/dev/shm` and `/run` with empty read-only tmpfs
  mounts, and refuses to do that unless it really is in a private mount
  namespace;
- drops to the image owner's uid and gid, with no supplementary groups. For a
  drive that is the instance's jail uid (`chownJail`). vmmd refuses a
  root-owned image or an owner outside the jail range. A subverted tool
  therefore holds exactly the authority that instance's own Firecracker
  process has;
- sets `no_new_privs` and these rlimits:
  - `RLIMIT_CPU` 60 s;
  - `RLIMIT_AS` 2 GiB;
  - `RLIMIT_FSIZE` the image size plus 1 MiB;
  - `RLIMIT_NOFILE` 64;
  - `RLIMIT_CORE` 0.

  `SIGXFSZ` is ignored, so an oversized write fails instead of killing the
  tool. Each run is also bounded by the caller's context, and its stdout and
  stderr are capped;
- applies a Landlock ruleset sized to the kernel's ABI:
  - read and execute on the system program and library directories;
  - read on `/etc` and `/proc`;
  - read and write on the image;
  - the session's scratch directories;
  - on ABI 4+, no TCP; on ABI 6+, abstract sockets and signals are scoped to
    the sandbox.

  A kernel without Landlock keeps the rest of the isolation; the editor's
  `RequireLandlock` setting turns that into a refusal.

Scratch lives under `<chroot base>/.guestdisk`, one private directory per
session, owned by the jail uid. Build output is the exception: it can be
several GiB, too large for that tmpfs. It is dumped into a directory beside
the export, which vmmd opens and hands to the tool as fd 3 (`/proc/self/fd/3`),
so builderd's directories need not admit the jail uid.

### 3. How a session edits

- **Journal first.** vmmd reads the superblock flags directly (fixed offsets
  in a 1 KiB block). If the drive needs recovery it runs
  `e2fsck -E journal_only -p`, the same replay a kernel mount performs, and
  requires the flag to be clear afterwards. This answers ADR-192's
  objection. External journals are refused.
- **Plan from an inspection.** One read-only debugfs run stats every path
  component a session may touch, under both layouts, and reads the
  full-rootfs marker. Creating a directory, replacing a file and removing a
  tree are each planned from that state. debugfs `mkdir` on an existing
  directory leaks an inode, and `write` refuses an existing name, so nothing
  is attempted blind.
- **Output the guest cannot forge.** debugfs prints guest-controlled bytes:
  file contents, symlink targets, xattr values, names. Every command in a
  script is bracketed by a marker command whose path carries the session's
  random nonce, and results are cut out between those markers. Only the
  first and third lines of `stat` output are read, which come before any
  guest-controlled field. `ls -p` records are split on `/`, which no name
  can contain.
- **Read everything back.** debugfs always exits 0 and reports failures only
  on stderr. The write script therefore ends with a probe of every file and
  directory it made, in the same run. Any stderr line beyond the version
  banner and the markers' own "not found" fails the edit, and so does any
  file whose kind, mode, owner, size or contents differ. The image is
  fsynced before the VM boots.
- **No link is followed.** A parent that is not a real directory, a symlink
  included, fails the write. A symlink, device or whiteout at a file's path
  is replaced, never written through. A name holding a control byte cannot
  be written on a script line, so a removal that meets one fails rather than
  stopping half done.

### 4. Semantics kept, and the deliberate changes

Unchanged:
- secrets.env, env.json, the app-task marker, sidecar env, the main manifest
  and the roster keep an existing regular file's owner and mode, as the
  `O_TRUNC` rewrite did;
- the resolver, the service-proxy CA and job.json are always fresh,
  root-owned files with their fixed modes;
- the restore skip ledger, its digest, and the "write nothing when the drive
  already holds the files" check work as before, now reading through the
  inspection;
- the fork scrub removes the same paths;
- export copies regular files and directories within the cap and drops
  everything else.

Changed:
- **A symlinked parent fails closed.** `os.Root` followed a symlinked
  directory that stayed inside the drive; the editor refuses it. The same
  now holds for a symlinked parent of the full-rootfs marker, which the job
  path already refused. A link there could otherwise move the staged files
  away from where guest-init reads them.
- **Fork scrub skips invalid names.** It removes `workloads/<name>/env.json`
  only for entries that are directories with a valid workload name. Those
  are the only ones vmmd writes.
- **Job staging creates a missing `/upper`.** On an optimized artifact it no
  longer refuses when `/upper` is absent.
- **Export replays the journal.** The old `ro,noload` mount read the builder
  drive as it stood before replay.
- **Sidecar manifests are refused.** `StageWorkloadManifest` on a sidecar
  drive (a shared read-only artifact) is now refused in both modes. Native
  mode already did, and no production path calls it.

### 5. Native mode

Staging keeps ADR-568's fence: the edit runs under the instance lock, only for
the original prepared owner (not revoked, launched or replaced), only on the
canonical writable drive, and only once every inherited loop mount, helper,
image source and TUN bind is retired. It writes no loop frame, because a
crash leaves no kernel mount behind. Recovery still retires and attests
frames an older vmmd left on an upgraded node. The retired producer is kept
in a metal-only test file so the native acceptance host keeps proving that
recovery.

## Consequences

- **The parser leaves the kernel.** A bug in libext2fs now gets a process
  with the jail uid, no network, a confined view of the filesystem and
  bounded resources. Reaching the host needs a second bug, in the kernel
  itself, through that much smaller surface.
- **Latency.** A restore with staged files makes three sandboxed runs:
  replay, inspection, and write with read-back. A cold boot makes two,
  because its fresh layer's journal is clean. An Ubuntu 24.04 VM measured
  about 14 ms per run, including namespace creation. ADR-192 measured the
  mount and unmount at 42 ms p50 and 85 ms p95. Native SSD nodes must be
  measured before this is called neutral. `pre_boot_replay_ms` in the
  operator restore record replaces `pre_boot_mount_ms` and
  `pre_boot_unmount_ms`; the other subphase keys are unchanged.
- **Host dependencies.** `e2fsck` joins `debugfs` as a required tool. Both
  ship in e2fsprogs, which every supported host has, and native
  qualification now checks for both.
- **Release.** `vmmd-guestdisk` is a new compute-lane binary (Makefile,
  packer, `release_units.json`, `pkg/releaseinstall`, the e2e harness and
  native qualification builds). vmmd N needs the helper from release N.
- **The guest contract is unchanged.** Files land where guest-init reads
  them, with the same modes.

## Follow-ups

1. builderd refreshes retained warm-builder drives with `debugfs -w` in its
   own process. That parse is in userspace but unsandboxed, and builderd
   holds database credentials. It should use guestdisk too: either vmmd does
   the refresh, or builderd runs the tool with Landlock and rlimits (it
   cannot create namespaces).
2. Measure restore and cold-boot staging latency on the native SSD nodes,
   and fold the inspection into the replay run if it matters.
3. Delete the native loop-frame recovery one release after every native node
   has run this version and reported no frames.
4. Consider a host-built per-instance config drive, or delivery over vsock
   after resume, so that restores need not edit the guest's drive at all.
   That changes the guest contract and the snapshot device layout, so it
   needs its own ADR.

## Rejected alternatives

- **More mount options.** No option stops the kernel from parsing the
  metadata.
- **fuse2fs.** It would keep the existing writers unchanged behind a FUSE
  mount. But it adds a host dependency whose journal support depends on its
  version, a long-lived server per session, per-operation round trips, and
  the kernel FUSE protocol as a new boundary.
- **A pure-Go ext4 library in vmmd.** Memory-safe parsing, but no available
  writer is proven on metadata_csum, flex_bg and htree images. A writer bug
  would corrupt tenant drives, and the parse would run inside the root
  daemon.
- **A host-built config drive or vsock delivery.** The better end state (see
  follow-up 4), but it changes the guest-init contract and the snapshot
  device list. It is not a fix that can ship now.
- **Re-executing vmmd as the sandbox.** Package init alone costs about 88 ms
  per run.
