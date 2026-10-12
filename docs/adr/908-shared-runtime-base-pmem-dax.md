# ADR-908: Shared runtime base through virtio-pmem DAX

- Status: Option 2 (per-account sharing) implemented, off by default behind `pmem_base_dir`. Option 1 (node-wide) still needs a security decision and is not implemented.
- Date: 2026-10-12
- Related: ADR-633, ADR-956, ADR-983, spec §4.6, §11
- Would amend: spec §4.4 boot config and §4.6 drive layout

## Context

Every guest boots from two drives (§4.6). Drive0 is the shared read-only
runtime base (Node, Python, libc), and drive1 is the app layer. Both are
virtio-blk, so every byte a guest reads from the base is copied into that
guest's own page cache, in its own RAM. Ten Node apps on one node hold ten
private copies of the parts of `node` they executed, and every snapshot
captures its copy. In production, 0–53 MB of each snapshot is runtime-base
blocks and 40–131 MB is app-layer blocks (ADR-956).

Firecracker 1.16 added virtio-pmem, and the guest kernel built from the
v1.17 6.18 config (ADR-983) has `VIRTIO_PMEM` and ext4 DAX. With the base
attached as a read-only pmem device and mounted with `dax`, a guest file read
maps the host's page cache of the base file directly instead of copying it.

## Measurements

Raw Firecracker 1.17, guest 6.18, n2 test node. Six VMs (1 GiB, 2 vCPU)
booted from the same read-only base holding Node 22 and an app with about
250 MB of state. Two interleaved rounds per variant:

| | virtio-blk root | pmem + DAX root |
|---|---|---|
| guest anonymous memory per VM | ~253 MB | ~205 MB |
| host `MemAvailable` drop per VM | 261 / 221 MB | 172 / 183 MB |
| snapshot non-zero content | 244–245 MB | 196–197 MB |
| restore → first byte | 74 / 103 ms | 78 / 82 ms |

Each VM stops holding its own ~48 MB copy of the runtime it executes, and
its snapshot shrinks by the same amount. Restore is unchanged; the restored
guest's DAX mappings point at the host page cache, which every VM on the
node keeps warm.

## The security question

With DAX, the guests of different tenants map the same physical pages of
the base. Shared physical memory between tenants allows cache side channels
such as Flush+Reload: one tenant can observe which base pages another
tenant's code touches. That is the same class of risk as cross-VM memory
deduplication (KSM), which hardened multi-tenant hosts disable. The base
holds public runtime code, so no secret data is shared, but access patterns
in shared library code, crypto routines included, can leak. Today guests
never map host memory in common: virtio-blk copies, and ADR-956 shares
blocks on disk, not mapped pages.

## Options

1. **Node-wide sharing.** Every VM maps the node's one copy of each base.
   This saves the most, but accepts the cross-tenant side channel above.
2. **Per-account sharing.** Each account gets its own reflinked copy of each
   base on a node (no extra disk on XFS). A copy has its own page cache, so
   pages are shared only among one account's VMs, and tenants stay isolated.
   Every VM still stops holding the runtime in its own RAM. The cost is one
   host page-cache copy of the touched base per account on the node, and
   the host can reclaim it.
3. **Keep virtio-blk.** No change.

Measured on the same node: six VMs, two accounts of three, on reflinked base
copies on XFS, two rounds each:

| | host cost per VM | host page-cache growth | guest anonymous memory per VM |
|---|---|---|---|
| virtio-blk | 256–262 MB | ~206 MB | ~254 MB |
| pmem, node-wide | 202–204 MB | 275–294 MB | ~205 MB |
| pmem, per account | 201–204 MB | 380–395 MB | ~205 MB |

Per-account sharing keeps the whole per-VM saving. The second account's copy
adds about 100 MB of reclaimable page cache.

Recommendation: option 2 if the density gain is wanted. Option 1 only after
an explicit security review accepts the side channel. Until then nothing
changes.

## Implementation notes (for option 1 or 2)

- vmmd attaches drive0 as `pmem` (`read_only`, `root_device`) on Firecracker
  1.16+, and adds `rootflags=dax`. Drive1 becomes `/dev/vda`, so guest-init's
  overlay setup must find its devices by role rather than by name.
- A snapshot records the pmem device, and its backing file must exist at
  the same path at restore. The base is cached per node like drives today
  (ADR-633). A snapshot taken with pmem restores only with pmem.
- A pmem base must be a multiple of 2 MiB.
- Hosts must keep the base file's page cache resident under memory pressure,
  or DAX faults turn into disk reads on every VM.

## Per-account implementation (2026-10-12)

Option 2 is implemented behind vmmd's `pmem_base_dir` (or
`FAAS_PMEM_BASE_DIR`). It is empty by default, which keeps every base on
virtio-blk.

- vmmd uses it only on Firecracker 1.16+, and never under native process
  recovery, whose image journal has no pmem slot.
- An app VM's base is cloned once per account with `FICLONE` into
  `<pmem_base_dir>/<account hash>/<base hash>.ext4`, and re-cloned when the
  base is newer. The directory must share a reflink-capable filesystem with
  the cached bases.
- The clone is attached as a read-only pmem root device mounted with `dax`,
  and drive0 is dropped. Guest-init reads `root=/dev/pmem0` from the kernel
  command line and finds the app layer and sidecars one letter lower.
- Inside the jail the clone is `base-pmem.ext4`; virtio-blk bases stay
  `base.ext4`. A restore stages both names when pmem is on. A pmem snapshot
  restored by a VMM without pmem cannot open `base-pmem.ext4`, fails to load
  and cold boots from virtio-blk (ADR-005). It never maps a base shared
  across accounts.
- A base qualifies only if its size is a multiple of 2 MiB and it is ext4
  with 4 KiB blocks: the kernel refuses `dax` on other block sizes, which
  would leave the guest without a root. Anything else, including a failed
  clone, boots from virtio-blk with a warning.
- Builders, networkless execution VMs, jobs and VMs without an account keep
  virtio-blk.

Qualified on the n2 node (Firecracker 1.17, guest 6.18). The base fixture
used 4 KiB blocks on reflink XFS, with guest-init from this change.
`TestMetalPmemAccountBase` passed:

- An app VM cold booted from its account's pmem copy, confirmed by
  Firecracker's `/vm/config`.
- It parked and restored from the pmem snapshot.
- A second account got its own clone.
- A VMM without `pmem_base_dir` restored the pmem snapshot by cold booting
  from virtio-blk.

`TestMetalParkWakeCycle` (100 cycles over virtio-blk with the new
guest-init) passed at p50 266 ms, p95 302 ms.

Follow-ups:

- **Base images:** bases are now built by `rootfs.BaseMkfsCommand`, with
  `-b 4096` and the size rounded up to an even MiB. Bases built before this
  change keep virtio-blk until the next runtime refresh rebuilds them; app
  layers keep `MkfsCommand`.
- **Garbage collection:** clones of bases that are no longer cached are not
  collected. Each clone shares its extents, so it costs metadata only until
  the base is deleted.
- **Metrics:** none yet for pmem boots or for fallbacks to virtio-blk.
