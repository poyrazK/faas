# ADR-906: Frozen instances

- Status: Proposed (vmmd prototype; schedd, gateway and host swap not wired)
- Date: 2026-10-12
- Related: ADR-005, ADR-009, ADR-071, ADR-074, ADR-149, ADR-159, ADR-680, ADR-983
- Builds on: the warm pool (`StateWarm`, #3067), which already resumes a
  paused VM in place
- Amends (when accepted): spec §6 state machine, §6.2 invariant 2 accounting,
  §4.7 billable states

## Context

An idle app parks after its plan's idle timeout (30/60/300/600 s). Parking
destroys the VM, so the app holds no RAM (invariant 4). It writes a snapshot
only when the deployment has no usable one: ADR-159 reuses an existing
capture, which takes about 520 ms of a paused guest to create. The next
request restores the snapshot. In prod over three days, vmmd restore took
p50 378 ms and p90 750 ms. Steady-state hits took p50 555 ms from admission
to first byte, and none came in under 350 ms.

The warm pool keeps paid, provisioned capacity as paused VMs (`StateWarm`)
and resumes them in place. Each one keeps its full plan RAM and vCPU
reservation and is billed while warm, so the pool is opt-in for customers
who pay for it.

Many apps come back within minutes of going idle. For them, a paused VM is
the fastest way back. It needs no snapshot load, network setup, drive
staging or resume hook, but today it costs the same host RAM as a running VM.
Two kernel and Firecracker features remove most of that cost:

- `memory.reclaim` (cgroup v2, Linux 5.19+) makes the kernel reclaim a
  given amount from one cgroup on request. A paused guest touches no memory,
  so its pages are cold. Clean pages mapped from the snapshot memory file are
  dropped and re-read on access. Written (anonymous) pages need swap.
- zswap or zram compress swapped pages in host RAM. A paused guest's pages
  compress well: idle Node pages measured 6–7× with lz4. With zswap
  writeback disabled (`memory.zswap.writeback = 0`, Linux 6.8+), tenant
  memory never reaches a disk.

Firecracker 1.17 (ADR-983) pauses vCPUs with `KVM_KVMCLOCK_CTRL`, so a guest
does not report soft lockups after a long pause. kvm-clock keeps the guest on
host time.

## Measurements

Earlier raw experiment, n2 test node, Firecracker 1.17, Node 22 app,
lz4 zram:

| | park + restore | freeze + thaw |
|---|---|---|
| leave serving | destroy (snapshot reused, ADR-159); ~520 ms create when none exists | pause + reclaim 10–20 ms |
| come back, after 3 s idle | restore + hook, first byte p50 ~190 ms | 4–37 ms |
| come back, after 10–40 s idle | same | 16–150 ms |
| host RAM while idle | 0 | ~2 MB cgroup + 1.6–3 MB compressed |

Without swap, reclaim drops only clean file pages. Prod running VMs carry
48–66 MB anonymous and 126–142 MB file memory, so a frozen VM on today's
swapless prod hosts would still hold its anonymous pages.

Prototype (`TestMetalFreezeThawCycle`, this ADR): see "Prototype results".

## Decision (proposed)

1. **A new instance state, `frozen`.** After an app's idle timeout, schedd
   freezes the instance instead of parking it, provided the app already has
   a usable snapshot or a cold-bootable rootfs (invariant 3). vmmd pauses
   the VM, stops its probes and reclaims its cgroup's memory. The VM keeps
   its lease, network namespace, jail uid and Firecracker process. A
   request thaws it in place, using the warm pool's resume path
   (`PromoteWarm`, the per-app concurrency gate), and probes restart.
2. **Frozen is not parked.** Invariant 4 is unchanged: a parked app holds no
   RAM. A frozen instance's resident footprint is measured after reclaim
   (`memory.current`, plus compressed bytes) and counted against a per-node
   frozen budget in `pkg/api/limits.go`. It does not count toward the
   47,600 MB serving ceiling (invariant 2). A thaw is admitted like a wake
   (plan RAM + 8 MB and vCPU). If admission fails, the instance is destroyed
   and the request takes the ordinary snapshot wake.
3. **Bounded and evictable.** A frozen instance parks after a frozen TTL, or
   earlier when the frozen budget, lease slots or host memory pressure need
   room. Eviction is a destroy, because the app already has its snapshot.
   Snapshots remain the source of truth (ADR-005): a host restart loses every
   frozen VM, and their apps wake from snapshots.
4. **Not billed.** Billing stays plan RAM + 8 MB per *running* second
   (§4.7). Frozen seconds are not billable, like parked ones. The warm pool
   stays billed because it reserves full capacity.
5. **No resume hook on thaw.** A frozen guest was never cloned, so its RNG
   streams stay unique (invariant 5) and kvm-clock keeps its clock. The hook
   still runs on every snapshot restore. A thaw of an instance whose image
   requires a healthcheck keeps the existing resume hook and healthcheck,
   as warm-pool resume does.
6. **Host swap is compressed and memory-only.** Compute hosts get zswap with
   writeback disabled for tenant slices. Without it, freezing still reclaims
   clean file pages; the frozen budget measures what is actually resident.
7. **Reclaim asks only for what can go.** vmmd requests the cgroup's file
   pages, plus its anonymous pages when the cgroup can swap, in 16 MiB
   chunks, and stops at the first `EAGAIN` or after 2 s. On the swapless n2
   node, one request for the whole charge kept the writer in the kernel for
   over two minutes while the kernel retried pages it could not free.

## Rollout

1. vmmd `Manager.Freeze` / `Thaw` and `JailerVMM.PauseVM` /
   `ReclaimVMMemory`, with unit and metal tests. This is the prototype.
2. Host zswap (`systemd_slices` / `host_hardening` roles) with
   `memory.zswap.writeback = 0` on tenant slices.
3. vmmd RPC, schedd `frozen` state (CHECK constraint, migration), frozen
   budget and TTL in `pkg/api/limits.go`, gateway thaw path, metrics
   (`vmmd_freeze_seconds`, `vmmd_thaw_seconds`, `node_frozen_resident_bytes`).
4. Canary on one node, comparing first-byte latency and host RAM against
   park/restore.

## Consequences

- A frozen instance still holds a slot, IP, jail uid and netns. These pools
  are sized for running VMs, so the frozen budget also caps the count.
- An instance-state change touches schedd, gateway, meterd and the billing
  trigger; that work needs its own reviewable PRs.
- Each frozen VM keeps a Firecracker process, about 2 MB of host RAM even
  after reclaim.
- Thaw latency depends on how much of the guest's working set the kernel
  reclaimed: the longer the hold and the more pressure, the more pages fault
  back in.

## Zswap sizing (measured 2026-10-12)

In the ADR-909 overcommit prototype, six freezes moved 318–573 MB each into
zram, and then the 3 GB device was full. zram's `disksize` caps the
*uncompressed* bytes, so the pool filled long before its RAM did. With zswap,
size the pool (`max_pool_percent`) by the compressed bytes the host can
spare. A node must stop freezing and start parking when the pool is full.

## At density (measured 2026-10-12)

`TestMetalFreezeDensity` (`pkg/fcvm`) froze twelve 1 GiB Node VMs on the
n2 node through vmmd's Manager, then thawed each and timed first byte and
a CPU-bound request. Swap was a 4 GB lz4 zram device:

| VMs | swap | VM cgroup per VM, before → after freeze | host memory freed | freeze p50 | thaw first byte p50 / p99 | CPU-bound request p50 |
|---|---|---|---|---|---|---|
| cold booted | none | 301 → 299 MB | ~0 | 1.3 ms | 4.7 / 12.6 ms | 14 ms |
| cold booted | zram | 307 → 1.5 MB | 2,056 MB, 171 MB per VM | 973 ms | 135 / 822 ms | 919 ms |
| restored from one snapshot | none | 12.1 → 10.8 MB | ~0 | 2.3 ms | 34 / 759 ms | 148 ms |
| restored from one snapshot | zram | 10.9 → 1.2 MB | ~0 | 50 ms | 83 / 200 ms | 68 ms |

What this changes:

- **Restored instances hold almost nothing of their own.** A VM restored
  from a snapshot reads its memory from the snapshot file, whose page cache
  all instances of the app share and the kernel can reclaim (ADR-908's
  one-memory-file bound: 52 MB per VM at six VMs). Its own cgroup held
  about 11 MB. A paused restored instance is therefore already cheap in
  memory, and reclaim adds little. Its real cost is its admission charge:
  1,032 MB under invariant 2, but the 64 MB floor under measured admission
  (ADR-909).
- **Cold-booted instances need swap to give memory back.** Their memory is
  anonymous. With zram, freezing freed 171 MB per VM after compression
  (about 2.2×), but thawing then faults the working set back: first byte
  p50 135 ms, p99 822 ms, and the first CPU-bound request took 919 ms.
- **Thaw beats restore when little was swapped.** Without swap, thaw
  reached first byte in 5–34 ms p50, against 130–300 ms for a snapshot
  wake. With swap, it stays below a restore at p50 but not always at p99.

So the frozen tier should freeze without reclaiming anonymous memory by
default. Instances restored from a snapshot are the common case, and for
them that keeps the latency win at almost no memory cost. Swap-backed
reclaim should be kept for host pressure, as in the ADR-909 overcommit
prototype, rather than applied to every freeze.

## Prototype results

`TestMetalFreezeThawCycle`, n2 test node (nested KVM), Firecracker 1.17,
guest 6.18, 512 MiB busybox guest restored from a snapshot, three
freeze/thaw cycles per run:

| host | hold | pause | reclaim | VM cgroup charge | thaw → first byte | snapshot wake → first byte |
|---|---|---|---|---|---|---|
| no swap | 3 s | 0.3–0.6 ms | 0.6–1.8 ms | 8 → 5 MiB | 8–13 ms | 312 ms |
| zram (lz4) | 3 s | 0.3–0.5 ms | 5–10 ms | 7 → 1 MiB, 3–4 MiB swapped | 13–19 ms | 318 ms |
| zram (lz4) | 20 s | 0.4–0.5 ms | 8–9 ms | 7 → 1 MiB, 3–4 MiB swapped | 18–24 ms | 288 ms |

A paused guest refused HTTP connections, and after each thaw its `Date`
header was within 2 s of host time. The restored guest's cgroup charge is
small because the snapshot file's page cache stays charged to the cgroup
that wrote it; apps with larger working sets move more pages to swap.
