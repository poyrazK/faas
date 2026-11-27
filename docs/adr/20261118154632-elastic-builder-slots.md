# Elastic builder slots: a hot per-node slot count, and borrowed slots that give memory back to tenants

Status: proposed

Amends: spec §4.5 ("Semaphore: 1 builder slot per 5 GiB parent cgroup"),
spec §12 (new metrics), CLAUDE.md "Builder slots". Every amendment applies only
while `builder_elastic_slots` is `on`; with it `off` (the default) builders
behave exactly as before. ADR-203's reserved builder slot is kept as it is.

## Context

Every compute node runs builds through builderd, which claims from one global
queue. A node's builder capacity is fixed in code and Ansible:

- builderd allows at most `maxBuilderSlots = 2` builds. The first is the
  guaranteed slot. The second opens only when the tenant residency gauge,
  scraped from schedd's metrics over HTTP every 2 s, is below 60% **and**
  faas-cp-build.slice has `2 × BuildVMRAMMB + BuildVMOverheadMB +
  BuilderSecondSlotHeadroomMB` (5,376 MiB) free.
- faas-cp-build.slice is 5120M and `CPUQuota=200%` on every node, under the
  6 GiB faas-cp.slice. It never has 5,376 MiB free while a build runs, so
  in practice each node runs one build. Today's two production nodes give the
  cluster two concurrent builds.
- Node sizing (ADR-203) reserves `BuilderSlotReserveMB` (2,048 MB) for that
  one slot out of the tenant slice. vmmd derives this at startup and
  registers the result in `compute_nodes`.

Changing capacity therefore means a code change, an Ansible change and a
restart, and the change is the same on every node. A large node and a small
node cannot run different numbers of builds.

Build demand comes in bursts. Average slot use is low (about 12% on the
2026-11-18 readout), but bursts queue builds behind one slot per node. In the
same period tenant memory sat far below its budget: measured tenant memory
peaked at about 29% of the admission ceiling. That memory could run builds when
tenants are not using it. The rule "builds never outrank tenant wakes" (spec
§4.5, ADR-203) must keep holding. A build may use tenant headroom only if it
gives it back as soon as tenants need it.

## Decision

### 1. A feature flag, default off

The new runtime config key `builder_elastic_slots` takes `off` or `on`. Its
default, `off`, lives in `pkg/api/limits.go`. builderd and vmmd both watch it.

- **`off`:** today's code path runs unchanged: the two-slot allocator, the 60%
  gate, the static slices and today's sizing. `builder_slots` (§2) may be
  stored but nothing reads it for decisions.
- **`on`:** §2–§8 apply.
- **Turning it off again:** builderd stops claiming builds on borrowed slots.
  Running borrowed builds finish. Give-back (§4c and §4d) stays armed while any
  borrowed builder exists.

### 2. A hot per-node slot setting

The runtime config key `builder_slots` holds a fleet default and per-node
overrides:

```json
{"default": {"reserved": 1, "max": 2},
 "nodes": {"gregale-compute-1": {"reserved": 1, "max": 8},
           "gregale-compute-2": {"reserved": 1, "max": 2}}}
```

- **Resolution:** each daemon picks its own entry by `FAAS_NODE_NAME`. A node
  not listed uses `default`. A missing key means `{"reserved": 1, "max": 2}`.
- **Validation in apid:**
  - `0 ≤ reserved ≤ BuilderReservedSlotsMax` and
    `reserved ≤ max ≤ BuilderSlotsMax` (16).
  - At most `BuilderSlotsNodeEntriesMax` node entries.
  - The value must give at least one reserved slot, in `default` or in some
    node entry.
  - `reserved` is 1 to `BuilderReservedSlotsMax` (4). A node runs the reserved
    slots it is provisioned with, not this value (amendment 1).
- **Clamping on the node:** each node clamps `max` to
  `floor(online CPUs / BuildVMVCPU)`. It reports both the requested and the
  applied values in its runtime config apply receipt and in metrics.
- **Applying without a restart:**
  - A raise opens slots at the next claim.
  - A cut stops new claims until the node's active builds drop below the new
    max.
  - A cut never cancels a running build.

### 3. Two kinds of slot

- **Reserved slots** are today's guaranteed slot:
  - They run in faas-cp-build.slice and are funded by ADR-203's
    `BuilderSlotReserveMB` out of tenant sizing.
  - They are never preempted.
  - The BuildKit seed run and warm builder captures use only reserved slots.
- **Borrowed slots** are `max − reserved`:
  - They run in a new slice, `faas.slice/faas-borrowed.slice`, beside
    faas-cp.slice and faas-tenant.slice. It is not under faas-cp.slice, so the
    control-plane fence does not move. The unit is `faas-borrowed.slice`: a
    dash in a slice name nests it, so the name carries only `faas-` in front.
  - No tenant capacity is set aside for them. They use memory the tenant
    slice may use but is not using, and give it back (§4).
  - A borrowed build never captures a warm builder snapshot, so its worst
    case is `BuilderMemoryMaxMB(BuildVMRAMMB)` (2,816 MiB).

A node fills its reserved slots first. A borrowed slot claims a build only
while every reserved slot on that node is busy.

### 4. Tenants first: how borrowed memory goes back

Every check reads the node's own cgroupfs and `/proc/meminfo`. None queries
the control plane. These are the inputs:

- `T`: faas-tenant.slice `memory.current`
- `Tmax`: faas-tenant.slice `memory.max`
- `B`: faas-borrowed.slice `memory.current`
- `A`: `MemAvailable`

**a. Admission (builderd).** A borrowed slot claims a build only when all of
these hold:
- `T + B + BorrowedBuildNeedMB ≤ BuilderBorrowAdmitPct (70%) × Tmax`, where
  `BorrowedBuildNeedMB` is the 2,816 MiB worst case plus 512 MiB of headroom
  (3,328 MiB).
- `A ≥ BorrowedBuildNeedMB + BuilderBorrowHostFloorMB`.
- No give-back has happened on the node in the last `BuilderBorrowCooldown`
  (60 s).

**b. Cooperative give-back (builderd, every 250 ms).** While
`T + B > BuilderBorrowGiveBackPct (85%) × Tmax` or
`A < BuilderBorrowHostFloorMB (1,024 MiB)`, builderd repeats this loop:
1. Cancel the newest borrowed build.
2. Wait for its VM to exit.
3. Read the inputs again.

The cancelled build returns to the queue at its original position through the
existing cancellation requeue (`requeueClaimAfterCancellation`). Its
`builds.preemptions` count goes up by one, and its build log says it gave its
memory back to running apps and will restart. Builds are cancelled one at a
time, so only what tenants need is freed.

**c. Forced give-back (vmmd, independent of builderd).** vmmd steps in when,
for 1 s, either `T + B > BuilderBorrowForcePct (92%) × Tmax` or
`A < BuilderBorrowHostCriticalMB (512 MiB)`. It then destroys the newest
borrowed builder VM itself, with the reason `preempted_for_tenants`. builderd
treats that exit as a preemption and requeues the build. This covers a
builderd that is stuck, slow or restarting.

**d. Kernel order.** vmmd starts every borrowed builder's Firecracker process
with `oom_score_adj = 1000`. If the host still runs out of memory, the kernel
kills a borrowed build before any tenant VM or daemon.

**The guarantee:** a borrowed build never causes a tenant VM to be OOM-killed,
refused admission or evicted.
- schedd, tenant admission (ADR-909) and invariant 2 do not change.
- Builders are not instances, and the tenant slice fence does not move.
- During a sudden spike, a tenant allocation may wait in kernel reclaim for one
  give-back, which takes about a second.

### 5. Preempted builds cannot bounce forever

- **After two preemptions:** a build that has been preempted
  `BuildMaxPreemptions` (2) times can be claimed only by a reserved slot.
  This uses the new column `builds.preemptions` (expand-only, `CHECK
  (preemptions >= 0)`) and a filter on the borrowed-slot claim query.
- **Time limits:** every attempt gets the full 15-minute build wall clock
  (ADR-791). Queue wait keeps counting from `EnqueuedAt`.
- **OOM requeue:** spec §4.5's "requeue once if `oom` and slot was
  opportunistic" applies to borrowed slots.

### 6. CPU and memory fences

- **Borrowed slice:**
  - faas-borrowed.slice runs at `cpu.weight = 10`, while tenants and the
    control plane keep the default 100, so tenants win any CPU contention.
  - Each borrowed builder's own scope carries the builder fences every
    builder has: `memory.max` = `BuilderMemoryMaxMB` and `cpu.max` = 2 vCPU.
    The slice itself has no `MemoryMax` or `CPUQuota` for vmmd to keep in
    step, so systemd's daemon-reload cannot undo one.
- **Reserved builders:** keep faas-cp-build.slice's `CPUQuota=200%`.

### 7. Ownership and compatibility

- **builderd** decides claims and runs the cooperative give-back.
- **vmmd** is the only component that touches Firecracker. It:
  - places borrowed builders in the borrowed slice, which ansible's
    `systemd_slices` role creates with its §11 fence pin;
  - sets their `oom_score_adj`;
  - runs the forced give-back, finding borrowed builders from the slice's
    scope directories, so a vmmd restart keeps it armed.
- **Builder create RPC:** gains an optional `borrowed` field, a new proto
  field that old vmmd ignores. builderd opens borrowed slots only when its
  local vmmd advertises the capability. Release N beside N−1 therefore never
  places a borrowed build in the reserved slice, where its 5120M fence would
  kill it.
- **vmmd restarts:** a lease rebuilt from vmmd's journal finds a borrowed
  builder's scope in faas-borrowed.slice, and adoption and native recovery
  accept that parent.
- **leakcheck:** learns the new slice.

### 8. Observability

- **Existing metric:** `builderd_build_slots_limit` keeps its name and reports
  the applied `max`.
- **New metrics:**
  - `builderd_build_slots_reserved`
  - `builderd_build_slots_active{kind="reserved|borrowed"}`
  - `builderd_build_preemptions_total{actor="builderd|vmmd"}`
  - `vmmd_builder_borrowed_memory_bytes`
- **Alert:** `FaasBuildPreemptionsHigh` fires when a node preempts more than
  `BuildPreemptionsAlertPerHour` builds in an hour. That means its `max` is too
  high for its tenant load.

## Consequences

- **Overcommit:** host memory can be overcommitted by `B`.
  - The protection for borrowed memory comes from give-back and kernel OOM
    order, not from a static partition.
  - Reserved build memory and control-plane memory stay statically
    partitioned, as ADR-203 decided.
- **Restarts:** under tenant pressure, borrowed builds restart from the
  beginning.
  - Layer reuse and the BuildKit seed make a restart cheaper, but it still
    costs build time.
  - Under sustained pressure only reserved slots make progress, which is the
    intended trade.
- **Today's 16 GB nodes:**
  - `Tmax` is about 10,100 MiB, so the 70% rule admits about one borrowed build
    at current tenant use.
  - `max: 8` needs at least 16 vCPU and a much larger tenant slice to fill.
  - The setting matters most on larger nodes and on nodes with little tenant
    load.
- **Per-node capacity:** a node whose tenants are busy gives builds back, while
  a quiet node takes more. The global queue spreads the work without any new
  scheduling logic.
- **Turning it on:** needs metal and leakcheck, because vmmd starts placing
  VMs in a new cgroup and killing them under pressure.
- **Implementation:** #7603 (limits, settings, `builds.preemptions`), #7604
  (vmmd), #7606 (builderd), #7607 (alert, runbook, ops guide, spec).

## Steps

0. *(Optional, independent of the flag; owner's call.)* Reserved builder VMs
   also get `oom_score_adj = 1000`. Today no VM has an OOM priority, so a
   host-wide OOM can pick a tenant VM over a builder.
1. This ADR.
2. Constants in `pkg/api/limits.go`. The `builder_elastic_slots` and
   `builder_slots` keys in apid's runtime config catalog, with validation.
   No daemon reads them yet.
3. The `builds.preemptions` migration and the claim filter.
4. vmmd:
   - the borrowed slice, placement, `oom_score_adj` and CPU limits;
   - the forced give-back;
   - the capability and adoption.

   Gated by the flag. Needs metal and leakcheck.
5. builderd:
   - watchers and per-node resolution;
   - the live limit;
   - borrowed admission and the cooperative give-back;
   - metrics.

   Gated by the flag.
6. Canary: turn the flag on and set one node to `max: 3`. Observe for at least
   48 h. Tenant OOM kills must stay at 0 and wake p95 must not change. Track
   preemptions per hour and build queue wait.
7. Allow `reserved ≠ 1`. Replaced by amendment 1: a node's reserved slots are
   provisioned at deploy time, not resized live.
8. A `gregalectl` command to read and set the setting, a runbook, and a
   `docs/` section.

## Alternatives

- **Reserve every slot out of tenant capacity.** Holds by construction, but
  each slot costs about 2.8 GiB of tenant capacity even while idle; 8 slots
  cost about 22 GiB. Kept for `reserved` only.
- **Grow faas-cp-build.slice and faas-cp.slice (today's opportunistic
  path).** A control-plane leak could then grow into build memory, and nothing
  gives memory back to tenants. Rejected.
- **`memory.low`/`memory.min` on the tenant slice, with no active give-back.**
  Builder guest memory is anonymous and the nodes have no swap, so the kernel
  cannot reclaim it. It would OOM anyway, just later. Rejected as the main
  mechanism; §4d keeps the kernel's last resort pointed at builds.
- **Freeze borrowed builders instead of cancelling them.** A frozen VM keeps
  its memory. Rejected.
- **Keep schedd's residency gauge as the signal.** It is scraped remotely every
  2 s and goes stale while schedd is unreachable, which is too slow and too far
  away for a give-back loop. The node's own cgroup reads replace it under the
  flag.

## Amendment 1 (2026-11-21): reserved slots are provisioned per node at deploy time

The owner asked for two guaranteed builds on each OVH compute node (16 threads,
128 GB). Step 7 planned that as a live change in vmmd. Three fences have to move
together for it, and only one of them is vmmd's to derive:

- the tenant slice and admission ceiling (`BuilderSlotReserveMB` per slot);
- `faas-cp-build.slice`, whose `MemoryMax` (5120M) holds one builder and whose
  `CPUQuota` (200%) is two CPUs;
- its parent `faas-cp.slice` (6G), which on a compute node both the
  systemd_slices role and `gregalectl manifest render` write.

vmmd writes no slice limits, and a direct cgroupfs write is undone by the next
daemon-reload. node_join already rewrites the tenant fence and vmmd's capacity
environment on every rollout from `gregalectl compute-nodes sizing`. So:

- **Decision.** A compute node's reserved builder slots are a deploy-time,
  per-node value: the manifest's `fleet.hosts[].builder_reserved_slots` (1 to
  `BuilderReservedSlotsMax` = 4, default 1). node_join passes it to
  `gregalectl compute-nodes sizing --builder-reserved-slots N` and writes, from
  the report:
  - the tenant fence and admission ceiling, holding back
    `ComputeNodeDaemonReserveMB + N × BuilderSlotReserveMB`;
  - `faas-cp-build.slice.d/90-builder-slots.conf`: `MemoryMax` =
    `api.BuilderSliceMemoryMaxMB(N)` (N × 2,816 MiB + 2,048 MiB for the one
    warm snapshot a node captures at a time + 256 MiB; 5,120 for N = 1 and
    7,936 for N = 2) and `CPUQuota` = N × 200%;
  - `faas-cp.slice.d/90-builder-slots.conf`: `MemoryMax` =
    `api.ComputeCPSliceMemoryMaxMB(N)` (6,144 MiB + 2,816 MiB per slot past the
    first), so the daemons keep today's 1,024 MiB beside the build slice;
  - `FAAS_BUILDER_RESERVED_SLOTS=N` for builderd and vmmd.
- **builderd** runs exactly N guaranteed slots. With the flag `off`, the fixed
  allocator's first N slots are guaranteed (no residency or memory gate) and
  slot N+1 stays opportunistic under the existing gates; N = 1 is today's
  allocator. With the flag `on`, `Applied().Reserved` is N whatever
  `builder_slots` asks for; a mismatch is logged. `max` is raised to at least N.
- **OOM attribution.** The slice-wide `oom_kill` delta is attributed to a build
  only while it is the slice's only builder. A build that shared the slice
  relies on its VM's exit status (137) through the failure-class table, so one
  builder's OOM no longer fails its sibling. The BuildKit seed run still reads
  the slice counter; a misattributed seed failure retries on its own schedule.
- **vmmd** funds the same N in its compute-only sizing derivation. It gains no
  runtime resize and no new cgroup writes.
- **Applying a change** takes a rollout of the node (node_join plus a daemon
  restart), never a hand restart: the slices, the tenant fence and vmmd's
  registered ceiling must change together.
- **Consequences.** Each slot past the first costs tenants
  `BuilderSlotReserveMB` (about 1.7 GB of admission ceiling) and lets
  `faas-cp.slice` hold 2,816 MiB more, which a control-plane daemon leak could
  also grow into, bounded by the slice. Builds still never outrank tenant
  wakes: the slots are funded before tenants are admitted.
