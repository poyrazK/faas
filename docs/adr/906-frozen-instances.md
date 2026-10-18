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
   (Amended 2026-10-15, as built: the `zswap` Ansible role, off by default
   per host, enables zswap with lz4 and no shrinker in front of a swapfile
   that only provides slots, sets `memory.zswap.writeback=0` and
   `MemoryZSwapMax` on `faas-tenant.slice`, and `MemorySwapMax=0` on the
   control-plane, builder, system and user slices and on `init.scope`. vmmd
   writes `memory.swap.max=0` on every running VM and opens swap only while a
   VM is frozen, and only when it finds zswap enabled, a swap device and
   writeback disabled on an ancestor of the VM's cgroup; the fence keeps the
   value. A freeze does not reclaim by default, so a thaw stays fast. Under
   memory pressure schedd first asks vmmd to compress a frozen VM
   (FreezeInstance with reclaim on a frozen instance): if that frees at least
   half its memory, the VM stays frozen and its reservation books the smaller
   footprint; otherwise, or on a host without the role
   (`frozen_compress_unavailable`), it parks as before.
   `schedd_frozen_compress_total{outcome}` counts the outcomes.)
7. **Reclaim asks only for what can go.** vmmd requests the cgroup's file
   pages, plus its anonymous pages when the cgroup can swap, in 16 MiB
   chunks, and stops at the first `EAGAIN` or after 2 s. On the swapless n2
   node, one request for the whole charge kept the writer in the kernel for
   over two minutes while the kernel retried pages it could not free.

## Rollout

1. vmmd `Manager.Freeze` / `Thaw` and `JailerVMM.PauseVM` /
   `ReclaimVMMemory`, with unit and metal tests. This is the prototype.
2. Host zswap (`systemd_slices` / `host_hardening` roles) with
   `memory.zswap.writeback = 0` on tenant slices. (Built 2026-10-15 as the
   `zswap` role; see decision 6.)
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

## Zswap at density (measured 2026-10-15)

`TestMetalFreezeDensity` again, on n2 with memory-only zswap set up as the
`zswap` role does (lz4, writeback off, swap closed to every other cgroup).
Twelve 1 GiB Node VMs were restored from one snapshot and then served
traffic (`FAAS_TEST_FREEZE_TRAFFIC`: four requests at a time per VM, JSON
reads with a CPU-bound request every 50th) before they froze:

| traffic | own (anonymous) memory per VM | swap | VM cgroup per VM, before → after freeze | compress p50 | thaw first byte p50 / p99 | first CPU-bound request p50 | kept compressed by schedd's rule |
|---|---|---|---|---|---|---|---|
| 60 s | 42 MB | none | 45.5 → 44.4 MB | — | 8.2 / 11 ms | 26 ms | — |
| 60 s | 42 MB | zswap | 44.7 → 12.2 MB | 111 ms | 33 / 39 ms | 157 ms | 12 of 12 |
| 180 s | 99 MB | zswap | 102.8 → 27.7 MB | 233 ms | 35 / 51 ms | 282 ms | 12 of 12 |

A snapshot restore of the same app reached first byte in 373–416 ms p50
and its first CPU-bound request took 401–409 ms.

- **Restored instances that have served traffic gain too.** After 60 s a
  restored VM owned 42 MB, close to prod's 48–66 MB. Without swap a freeze
  freed none of it. zswap held it at 3.7–3.9× (503 MB in a 130 MB pool):
  32 MB freed per VM after 60 s and 74 MB after 180 s, and every VM passed
  schedd's rule (at least 16 MB and half its memory freed).
- **Cold-booted instances** (about 300 MB anonymous) went from 310 to
  120 MB per VM at 2.5× (3,566 MB in a 1,426 MB pool). Compressing took
  0.88 s p50, and thaws reached first byte in 83 ms p50 (241 ms p99), with
  556 ms for the first CPU-bound request.
- **A compressed thaw stays well ahead of a restore.** Against the
  uncompressed thaw (8 ms to first byte, 26 ms for the CPU-bound request)
  it adds about 25 ms to first byte and 130–260 ms to the first CPU-bound
  request, so compression stays a fallback for memory pressure and a
  freeze still does not reclaim by default.
- **Nothing else swapped.** With `init.scope` also at `MemorySwapMax=0`,
  no cgroup outside the tenant VMs held swap after the runs.

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

## Pause on idle (measured 2026-10-12)

A raw Firecracker 1.17 harness on the n2 node (guest 6.18, Node 22 app,
1 GiB guest) put a small gateway in front of each VM. The gateway resumed
the VM on a request and paused it after an idle threshold, without
reclaiming memory. This tests freezing seconds after the last request
rather than at the idle timeout.

Cost of the pause:

- Pause and resume calls took 0.4 ms each (p99 1.7 and 4.1 ms). 1,000
  cycles had no failures, and a snapshot of the cycled VM restored and
  served.
- A request that found the VM paused took 1–3 ms longer at p50 (2 s
  threshold, gaps of 3–30 s). A CPU-bound request right after resume took
  the same 9 ms as on a VM that was never paused. A VM restored from a
  snapshot reached first byte 5–14 ms after resume, occasionally ~100 ms.
- A paused VM used no host CPU. An idle running Node VM used about 107 ms
  of CPU every 30 s.
- With a 2 s threshold the VM was paused 48% of the time at 3 s request
  gaps and 84% at 10 s gaps, resuming about 10 times a minute. An app with
  a request every second gains nothing.

What stops while paused:

- Guest timers do not fire. Resuming the VM briefly on a schedule
  ("breathing") does not make them fire on time.
- After resume the guest clock ran a constant ~75 ms behind the host. The
  offset was the same after 3, 30 and 120 s pauses, so it does not drift.
- A plain outbound connection survived a 5 min pause. A server with an 8 s
  keepalive dropped it after pauses longer than ~8 s unless the VM breathed
  for 50 ms every 2 s (~0.35% of a core). A server with a 60 s idle timeout
  closed it.
- Inbound traffic that bypassed the resume path, either a new connection or
  a kept-alive one, timed out after 3 s. Breathing 50 ms every 1 s brought
  it to 716 ms.
- A response stream paused mid-flight resumed, but the client saw a gap
  as long as the pause.

Under load on the 2-vCPU host:

- Resuming 11 VMs at once: first byte p50 19–73 ms, max 28–318 ms. Twelve
  concurrent snapshot restores on the same host took 7–10 s.
- 11 VMs with Poisson arrivals at a 5 s mean gap: p50 6.4 ms against 3.1 ms
  for VMs that were never paused, max 906 against 436 ms.

So a short threshold is cheap per request, but only safe when every ingress
path resumes the VM (gateway, internal service calls, triggers and
kept-alive connections) and nothing is in flight: no open request, stream
or websocket. Apps that depend on timers, background workers or
connection keepalives would need an opt-out, or a threshold no shorter than
today's idle timeout.

## Economics (simulated 2026-10-12)

Production traffic is internal testing, so the simulation replays the
public Azure Functions 2019 trace: 3,000 randomly sampled HTTP-triggered
apps over 7 days, 665 million invocations, with arrivals spread uniformly
inside each minute. Each app gets a plan at random in the financial
model's mix (80% Free; paid apps 55/40/5 Hobby/Pro/Scale) with today's
prices, idle timeouts and included GB-h. A request that finds a frozen
instance waits 8 ms (measured thaw), a parked app 340 ms (prod restore
p50) and a cold one 3 s. Admission is measured (ADR-909), frozen instances
are charged at its 64 MB floor and hold no vCPU slot. A node's capacity is
the most apps whose combined state, sampled at 2,000 instants, stays inside
every limit at the 99.9th percentile. The simulator is in
`scripts/experiments/frozen-tier-economics`.

| policy | billed GB-h per app-month | revenue per app-month | wakes served by thaw | mean wake latency | apps per EX44 (€44) | infra per app, EX44 | apps per n2-standard-4 (~€130) | infra per app, n2 |
|---|---|---|---|---|---|---|---|---|
| today: idle timeout, then park | 29.4 | €3.94 | 0% | 340 ms | 251 | €0.175 | 42 | €3.10 |
| freeze at the idle timeout, park after 1 h (this ADR as proposed) | 29.4 | €3.94 | 99.1% | 10.9 ms | 251 | €0.175 | 42 | €3.10 |
| freeze after 2 s, park after 1 h | 9.3 | €3.83 | 99.8% | 8.5 ms | 716 | €0.061 | 118 | €1.10 |
| same, frozen time billed | 67.2 | €4.25 | 99.8% | 8.5 ms | 716 | €0.061 | 118 | €1.10 |
| freeze after 2 s, park after 10 min | 9.3 | €3.83 | 99.1% | 11.0 ms | 716 | €0.061 | 118 | €1.10 |
| freeze after 10 s, park after 1 h | 16.1 | €3.87 | 99.6% | 9.3 ms | 395 | €0.111 | 69 | €1.88 |
| freeze after 0.5 s, park after 1 h | 6.5 | €3.81 | 99.9% | 8.3 ms | 1,010 | €0.044 | 170 | €0.77 |

What it shows:

- **vCPU slots are the binding limit in every policy**, not RAM (160 slots
  on an EX44, 32 on an n2-standard-4: host threads × `CPUOvercommit`).
  Measured admission alone leaves today at 251 apps per EX44.
- **Freezing at the idle timeout buys latency, not capacity.** The
  instance still runs, holding its vCPU slots, through the whole 30–600 s
  idle timeout. Capacity comes from freezing soon after the last request:
  a 2 s threshold fits 2.85× the apps on the same node.
- **Revenue barely moves if frozen time is unbilled.** Customers stop
  paying for the idle tail, so billed GB-h falls to a third, but
  subscriptions dominate revenue: overage falls from €0.16 to €0.05 per
  app-month and revenue by 3%. On an EX44 the infra saving offsets that
  almost exactly; on an n2-standard-4 the margin per app rises from €0.85
  to €2.73. Billing frozen time would raise bills above today's, because an
  instance stays frozen for up to an hour.
- **The threshold is the lever.** 0.5 s fits 41% more apps than 2 s but
  resumes 1.7× as often (11,924 against 6,901 wakes per app-week; today has
  1,096 restores).
- **The frozen footprint barely matters.** With a frozen instance keeping
  5%, 15% or 50% of its running RAM, the results are identical, except at
  0.5 s with 50%, where RAM binds first (861 apps per EX44, 146 per
  n2-standard-4).

Limits of the simulation:

- The trace is serverless functions from 2019, not long-running services.
  Services that are busy, or exempt because of background work, gain less.
- Plans are assigned at random, not by how busy an app is.
- The 7-day window never reaches the cold tier.
- Thaw is assumed to find a free vCPU slot. At the capacity limit that
  fails in up to 0.1% of instants; what the thaw does then is not modelled.

The decision above freezes at the idle timeout. Freezing within seconds is
what buys capacity, and it needs the conditions in "Pause on idle". That
choice is still open.
