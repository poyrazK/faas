# ADR 20261031143521 · Compress frozen VMs before RAM runs out

- **Status:** proposed.
- **Date:** 2026-10-31
- **Amends:** ADR-906 decision 6 (when schedd compresses a frozen VM),
  ADR-909 (the measured charge of a frozen instance)
- **Related:** ADR-1151 (pause tiers), ADR 20261029151111 and ADR
  20261030100950 (CPU admission by use), #6118 (zswap on the fsn-3 canary)
- **Amended by:** ADR 20261031190030 (frozen VMs then yield their memory to
  admission; a frozen instance's measured charge uses its latest sample)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

CPU admission by use (ADR 20261029151111, ADR 20261030100950) moved the
binding limit on a dense node from CPU to RAM. Read-only measurements on
production on 2026-10-31 (rc.311, fsn-2 and fsn-3, ten VMs):

- Compute nodes have no swap, no zram, and zswap disabled. vmmd's frozen-VM
  compression (ADR-906 decision 6, `pkg/fcvm/freeze.go`, `frozen_swap.go`)
  needs the `zswap` role, which no production node ran until #6118 turned it
  on for fsn-3.
- A frozen VM keeps all its memory: 18-144 MB each, median about 50 MB,
  11-44 MB of it anonymous. ADR-906 measured a compressed one at about 2 MB
  of cgroup plus 1.6-3 MB compressed (lz4, 6-7x).

Two rules keep that memory booked even where zswap exists:

1. **Compression is only an eviction step.** schedd asks vmmd to compress a
   frozen VM (`FreezeInstance` with reclaim) only from `EvictUnderPressure`,
   which runs when a node's *actual* memory exceeds its ceiling. An idle
   freeze does not reclaim (`pkg/sched/frozen.go`, `reclaim=false`).
   Admission keeps charges under the ceiling, and a charge is at least the
   memory it measures, so a full node refuses wakes long before its actual
   memory crosses the ceiling, and nothing is compressed.
2. **A frozen VM is charged like a running one.** The memory shadow charges
   every instance that counts for RAM `api.MeasuredChargeMB`:
   `max(memory.current, 64 MB) x 1.25`. A compressed 5 MB VM still books
   80 MB, and `ShrinkFrozenCharge` never goes below the same 64 MB floor.

The ADR-1151 simulator with production's admission (quiet pause plus
measured CPU at 700%), the frozen tier, a 16-thread / 128 GB node and the
Azure trace:

| RAM model | Apps per node | Binds |
|---|---|---|
| Production today (no compression, 64 MB floor) | 749 | RAM |
| Every frozen VM compressed, 64 MB floor | 1,068 | RAM |
| Every frozen VM compressed, 8 MB frozen floor | **1,092** | CPU |

## Decision

1. **A frozen instance's floor is `FrozenChargeFloorMB` (8 MB), not 64 MB.**
   The 64 MB floor leaves a running instance room for its next request; a
   frozen VM is paused and cannot grow, and a thaw re-admits its full
   reservation like a wake (`promoteFrozenInstanceLocked`), parking it when
   the node cannot fit it. The frozen charge is
   `min(plan + 8, max(memory.current, 8 MB) x 1.25)`:
   - in the memory shadow (`instancestats`), for instances in `frozen`;
   - in the ledger (`frozenChargeMB`, `Freeze`, `ShrinkFrozenCharge`,
     `admitFrozen_locked`). The frozen seed after a schedd restart has no
     measurement and keeps booking its 64 MB stand-in until the next
     sample.
   It applies whether or not the VM is compressed: an uncompressed frozen VM
   is charged its real memory too.

2. **schedd compresses frozen VMs before a node fills.** On each reaper
   tick, a node whose admission usage (the figure `Admit` compares with the
   ceiling) is at least `FrozenCompressHighWaterPercent` (85) of its ceiling
   has its frozen VMs compressed, longest frozen first, until the usage the
   compressions free brings it under `FrozenCompressLowWaterPercent` (75):
   - only VMs frozen at least `FrozenCompressMinAgeSeconds` (60). A VM
     thawed soon after its freeze keeps the fast thaw (4-37 ms
     uncompressed, against 16-150 ms compressed and a ~190 ms snapshot
     restore, ADR-906);
   - at most `FrozenCompressPerNodePerTick` (4) per node per tick, off the
     request path: a reclaim can take up to 2 s (`reclaimBudget`);
   - each VM once. The ledger remembers it was compressed; compressing again
     frees little (ADR-906);
   - a compression that is unavailable (no zswap on the node,
     `frozen_compress_unavailable`) or frees too little leaves the VM frozen
     as it was. It does not park it: unlike eviction, nothing here needs the
     memory yet. A node that reports `unavailable` is not asked again for
     `FrozenCompressUnavailableBackoffSeconds` (600).
   After a compression the reservation books the smaller footprint, as
   `EvictUnderPressure` already does, and the next vmmd sample shows it in
   the shadow.

3. **Eviction is unchanged.** Above the ceiling, `EvictUnderPressure` still
   compresses a frozen victim first and parks it when that frees too little.

4. **Switch.** schedd `frozen_proactive_compress`, on by default: zswap is
   the per-node gate (the `zswap` role and its canary list, #6118), and
   without it every attempt is `unavailable` and backs off. `= false` in
   `schedd.toml` or `FAAS_FROZEN_PROACTIVE_COMPRESS=false` turns it off.

5. **Metrics.** `schedd_frozen_compress_total{outcome}` keeps its outcomes
   and gains a `trigger` label (`pressure`, `proactive`). A new counter is
   not needed; dashboards that sum by outcome keep working.

## What does not change

- Invariant 2. Charges stay under the ceiling; a thaw still re-admits the
  full reservation, so a decompressing VM never runs on memory the node did
  not book.
- The durable node reservation: it does not count `frozen` instances, so no
  migration is needed.
- Billing: a frozen instance stays unbilled (ADR-1151).
- The request path: compression runs on the reaper tick.

## Rollout

- fsn-3 has zswap after its first release with #6118; fsn-2 joins its
  canary list once fsn-3 has compressed and thawed VMs cleanly.
- Watch `schedd_frozen_compress_total{trigger="proactive"}` by outcome,
  thaw latency for compressed VMs (`wake.thaw_breakdown`), and the zswap
  pool size.

## Rejected alternatives

- **Compress at freeze.** Every thaw would pay 16-150 ms instead of 4-37 ms,
  including the ones right after a freeze, and every freeze would pay the
  reclaim, whether or not the node needs the memory.
- **Compress when admission refuses.** It would put up to 2 s of reclaim on
  a wake's path.
- **Lower the floor for every instance.** A running instance's floor is the
  room its next request needs; only a paused one cannot grow.

## Validation

- Ledger and shadow: a frozen instance is charged with the 8 MB floor, a
  running one with 64 MB; a thaw still re-admits the full reservation.
- Proactive compression: it starts at the high-water mark and stops at the
  low-water mark, takes the longest-frozen VMs at least 60 s old first,
  never more than four per node per tick, never the same VM twice; an
  `unavailable` node backs off and nothing parks.
- Eviction above the ceiling behaves as before.
