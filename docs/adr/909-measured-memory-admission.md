# ADR-909: Admission by measured memory

- Status: Accepted (owner decision, 2026-10-12). Measured admission is on
  by default in schedd.
- Date: 2026-10-12
- Related: ADR-906, ADR-907, ADR-193, spec §1, §4.7, §6.2 invariant 2
- Amends: spec §6.2 invariant 2

## Context

Invariant 2 admits a VM only while Σ(plan RAM + 8 MB) over live instances
stays under the node's ceiling: 47,600 MB on the reference box, 8,593 MB on
the 16 GB production compute nodes. A 1 GiB VM therefore books 1,032 MB
whatever it uses. Billing charges the same plan RAM per running second
(§4.7); this ADR does not change billing.

Measured use is far below the booking:

- Production, read-only cgroup scan, 2026-10-12: four running VMs used
  499 MB of 3,616 MB reserved (14%). Of that, 188 MB was anonymous memory
  and 302 MB was page cache the kernel can reclaim.
- Until ADR-907, memory a guest freed stayed resident. With free page
  reporting the host gets it back within seconds, so use now tracks the
  working set.

## Evidence

n2 test node (16 GB, 2 vCPU, no swap), Firecracker 1.17, guest 6.18, free
page reporting on. Twenty 1 GiB VMs ran a Node app with about 250 MB of
state each: 20,640 MB of bookings against the 8,593 MB a production node of
this size admits, or 2.4×.

| | VM memory | host MemAvailable | memory PSI some (avg10) | request p50 / p99 |
|---|---|---|---|---|
| before boot | — | 10,097 MB | 0 | — |
| steady (20 VMs) | 5,175 MB (259 MB/VM) | 4,996 MB | 0 | 2.7 / 4.8 ms |
| burst: 10 VMs +320 MiB each | 8,384 MB | 1,783 MB | 0 | 2.9 / 4.7 ms |
| 8 s after the burst is freed | 5,637 MB | 3,522 MB | 0 | 2.8 / 18.9 ms |

Every VM stayed alive and served correct results. The burst was capped so
the shared node kept 1.5 GB free. Had all twenty VMs grown to their plan
RAM at once, they would have needed 20.6 GB on a 16 GB host. That is the
risk this ADR must bound.

## Proposal

1. **Charge measured memory, capped by plan.** A running instance charges
   min(plan RAM + 8 MB, max(p95 of its cgroup's `memory.current` minus
   reclaimable file pages over the last 10 minutes, a floor)) + headroom. A
   new instance charges its plan until it has a history. Per-VM
   `memory.max = plan + 8 MB` is unchanged, so no VM can exceed its plan.
2. **Bound the overcommit.** Σ(plan + 8) may exceed the ceiling by at most a
   node factor (2× to start), and Σ(charges) must stay under the ceiling.
   Both live in `pkg/api/limits.go`.
3. **Evict before the kernel does.** When node MemAvailable falls under a
   floor, or memory PSI rises, schedd stops admitting and then freezes
   (ADR-906) or parks the coldest instances until the floor is met. vmmd and
   the control plane keep a protected `oom_score_adj`. Compressed swap with
   writeback disabled (ADR-906) gives eviction time.
4. **Billing is unchanged.** Customers keep plan-based, predictable bills.
   The financial model gains capacity per node, which is the business
   decision this ADR asks for.

## Hard reclaim: virtio-mem

Free page reporting returns only memory the guest has freed. Firecracker
1.17's virtio-mem device lets the host take memory back from a guest that
still holds it. Measured on the n2 node with guest 6.18: a Node VM had
512 MiB of boot memory plus a 1 GiB hotplug area, onlined as movable
(`memhp_default_state=online_movable`).

| step | time | VM memory on the host |
|---|---|---|
| boot | — | 305 MB |
| plug 768 MiB (`PATCH /hotplug/memory`) | 66 ms | 316 MB |
| app allocates 640 MB beyond its boot memory | — | 961 MB |
| app frees it (no free page reporting) | — | 965 MB |
| unplug to 0 | 272 ms | 318 MB |

The app kept serving after the unplug. Under this ADR, a VM could boot with
its measured working set plugged and grow to plan RAM on demand. Unplugging
then enforces measured charges, and gives eviction a step short of freezing
or parking.

## Shadow mode (implemented 2026-10-12)

schedd computes both bookings without changing admission. Every
instance-stats tick (200 ms) sums, per node, over the instances invariant 2
counts:

- `schedd_node_memory_reserved_mb`: plan RAM + 8 MB, what admission books
  today;
- `schedd_node_memory_measured_mb`: `api.MeasuredChargeMB`, the instance's
  cgroup memory (at least `MeasuredChargeFloorMB`, 64 MB) plus
  `MeasuredChargeHeadroomPercent` (25%), capped at plan RAM + 8 MB. An
  instance without a sample books its full reservation.

Their ratio on production nodes, over real traffic, is the evidence this ADR
needs before admission itself changes.

## Enforcement behind a flag (implemented 2026-10-12)

`measured_admission = true` in schedd's config switches RAM admission to
the shadow figures. It is off by default, so invariant 2 holds unless the
owner turns it on. With it on, a request fits a node iff
`used + request ≤ ceiling`, where `used` is the larger of:

- the node's measured charge from the latest tick (no older than
  `MeasuredAdmissionSampleMaxAgeMillis`, 2 s), plus the full reservation of
  every instance admitted after that tick, which the measurement cannot see;
- the node's reservations minus the overcommit allowance, so Σ(plan + 8)
  stays under `MeasuredAdmissionMaxOvercommitPercent` (200%) of the ceiling.

A node without a fresh measurement falls back to its reservations. The
placement chooser floors vmmd's live report with the same figure
(`NodeLedger.AdmissionUsedMB`), so placement and admission agree.

`TestMetalMeasuredAdmissionDensity` (`pkg/fcvm`) runs the ledger against
real VMs. It boots 1 GiB Node VMs through vmmd's Manager and admits each
one by their cgroups' `memory.current`. On the n2 node, against an
8,593 MB ceiling, two runs gave:

- 16 VMs admitted, where reservations allow 8. The 17th was refused by
  the overcommit cap.
- Measured charge 5,987–6,016 MB. Host MemAvailable fell from 10.0 GB to
  3.9 GB, with memory PSI 0.
- Five VMs then grew by 320 MB each. The measured charge rose to 7,914 MB,
  host MemAvailable stayed at 2.4 GB, PSI stayed 0, and all 16 VMs served.

Unit tests in `pkg/sched/admission_measured_test.go` cover the same rule,
including random admit/release traffic.

## Charge history and pressure eviction (implemented 2026-10-12)

- **Charge history (proposal 1).** The instance-stats poller records each
  instance's resident memory every `MeasuredChargeSampleSeconds` (5 s) over
  `MeasuredChargeWindowSeconds` (10 minutes). The charge starts from the
  larger of the latest sample and that window's p95
  (`MeasuredChargePercentile`). A VM that just freed memory therefore still
  books its recent peak, while growth is charged at once.
- **Pressure eviction (proposal 3).** Under measured admission, the reaper
  reads each node's tenant memory from vmmd's capacity report. When it is
  over the node's admission ceiling, the reaper evicts in the existing
  RAM-pressure order until the node is under `MeasuredEvictionTargetPercent`
  (90%) of the ceiling. That order skips service replicas and instances
  younger than 30 s, evicts Scale last, and is otherwise LRU. Each eviction
  counts the instance's measured memory. Evictions are counted under
  `schedd_eviction_fired_total{reason="measured_memory"}`. Without measured
  admission this never fires: every VM's `memory.max` keeps a node under its
  reservations.
- **On the n2 node,** `TestMetalMeasuredAdmissionDensity` ran the selector
  on its 16 VMs after the burst. The ceiling was set just under the VMs'
  real cgroup total, 6,318 MB against a 6,118 MB ceiling. The selector chose
  the two least recently used VMs. Destroying them brought the node to
  5,109 MB, under the 5,506 MB target, and the other 14 kept serving.

## Decision (accepted 2026-10-12)

The owner accepted this ADR. schedd's `measured_admission` now defaults to
on, and `measured_admission = false` restores reservation admission.
Invariant 2 now reads: a node's measured memory stays under its admission
ceiling, Σ(plan RAM + 8 MB) stays under twice that ceiling, and schedd
evicts when measured memory crosses the ceiling. Billing is unchanged: plan
RAM + 8 MB per running second.

What still bounds the risk:

- Every VM keeps `memory.max = plan + 8 MB`, so no VM can grow past its
  plan.
- The tenant slice's hard `memory.max` sits above the admission ceiling
  (57,344 MB against 47,600 MB on the reference box). Pressure eviction
  runs on the reaper's 10 s tick from vmmd's 1 s capacity reports. A burst
  that outruns eviction ends in the kernel OOM-killing a tenant VM inside
  the slice, never the control plane. The app then wakes again from its
  snapshot.
- The financial model's node capacity assumes reservations. It now
  understates how many tenants a node holds, until the owner updates it.

Still open:

- Eviction destroys; it does not freeze. Freezing (ADR-906) would keep the
  evicted VMs a fast thaw away.
- The warm floor still books full reservations (`HeadroomMB`), so it never
  pre-warms into the overcommit.
- Measurements come from vmmd. A vmmd that under-reports can over-admit
  its own node up to the overcommit cap; vmmd already runs as root there.

## More reclaim levers, measured (2026-10-12)

- **virtio-mem at scale:** six Node VMs served requests round robin while
  each plugged 768 MiB, used 576 MB of it, freed it and unplugged.

  | step | per-VM time | memory | request p50 / p99 |
  |---|---|---|---|
  | baseline | — | — | 1.4 / 5.3 ms |
  | plug + use | 195–413 ms | VMs 5,059 MB | 1.3 / 14.7 ms |
  | unplug | 1.7–1.9 s | VMs 1,586 MB | 1.9 / 413 ms |

  Unplug returned 3.5 GB but stalled requests while the guests migrated
  pages, so it suits idle VMs, not serving ones.
- **Balloon inflation:** taking 400 MiB from a guest holding 576 MB took
  155 ms and dropped the VM to 299 MB on the host. With `deflate_on_oom` the
  app could still grow by 576 MB: the balloon deflated to 193 MiB, though
  the growth took 4.0 s. Without it the guest kernel panicked ("System is
  deadlocked on memory"). A balloon must keep `deflate_on_oom`.
- **Overcommit with freeze-based eviction** (raw prototype of proposal 3):
  - Setup: twenty 1 GiB Node VMs with free page reporting (5.0 GB
    available at steady state), then each grew by 320 MB in turn, about
    11.6 GB of demand on ~10 GB. Before each growth, if MemAvailable would
    drop under 2.5 GB, the least recently grown VM was frozen (paused,
    memory reclaimed into 3 GB of zram).
  - Six freezes freed 318–573 MB each in 0.7–2.1 s. After that the zram
    device was full and further freezes freed nothing.
  - MemAvailable bottomed at 1.1 GB. The kernel OOM-killed nothing and all
    twenty VMs stayed alive.
  - Thawed VMs reached first byte in p50 163 ms, max 204 ms.
  - Eviction must therefore track compressed-swap capacity, and park
    (destroy) once it is full.

## Validation plan

- Property tests: the charge never exceeds plan + 8 MB, Σ(plan + 8) never
  exceeds factor × ceiling, and eviction restores the floor.
- Metal: grow every VM on a node to plan RAM at once. The host must stay up
  through freeze/park eviction, with no kernel OOM kill of vmmd or the
  control plane.

## Amendment 1 — The durable node reservation honours the overcommit bound (2026-10-26)

**Problem.** ADR-193's durable per-node reservation (the
`admission_reserve_node` function and its statement and MemStore mirrors)
refused any instance once Σ(plan RAM + 8 MB) on a node passed
`admission_ceiling_mb`. Measured admission never reached the 2× bound this
ADR accepted: every admission past the bare ceiling failed inside the insert.
Placement made it worse. vmmd reports measured RSS only to its own node's
schedd, so that schedd saw headroom on its node, kept choosing it, and the
insert refused with `ErrNodeCapacity`. Placement retried only a stale
placement (ADR-1528), so the wake failed with a capacity Problem and a
gateway wake parked another tenant's idle instance (ADR-643), even when a
peer node had room. `TestWakeFailover_PropertyKeepsInvariants` checked only
the 2× upper bound, so it did not catch the 1× cap.

**Decision.**

1. The placement binding (`state.NodeAdmission`) carries a reservation
   allowance. schedd sets it from its ledger when it binds the placement:
   `MeasuredAdmissionMaxOvercommitPercent` while a fresh measurement of that
   node drives admission, otherwise 100. The durable guard bounds
   Σ(ram_mb + overhead) by that percent of the ceiling, clamped to
   [100, 200]. An unbound or reservation-only admission keeps the bare
   ceiling, as do migrations and qualification admissions.
2. Only the scheduler that measures a node can book past its ceiling. A
   peer's placement reads the reservation sum from the database against the
   bare ceiling, and its admissions carry 100, so a peer never adds to a
   node above its ceiling.
3. A durable capacity refusal is re-placed: placement excludes the refused
   node and chooses again, within `NodeAdmissionPlacementAttempts`. No VM
   call or ledger admission precedes the insert, so the retry has no side
   effect to undo. When no other node fits, the original refusal is
   returned, as before.
4. Migration `20261026090412734` adds `admission_reserve_node(…, integer)`
   and `admit_wake_instance(…, integer)`. The previous signatures stay for
   the previous release and delegate at 100, so release N-1 behaves exactly
   as before. schedd uses the functions only when the new signatures exist
   and otherwise takes the statement path, which applies the same allowance.

**Bound.** Invariant 2 as accepted above now holds as written: the ledger
keeps a node's measured charge under its ceiling, and the database keeps
Σ(plan RAM + 8 MB) under twice it. The window in which the measuring
scheduler misses a peer's new admission is one stats tick: the next tick
books that instance at its full reservation until it has an RSS sample.

**Validation.** `checkReservationAllowance` (MemStore, PostgreSQL function
and statement paths), `TestMeasuredAdmissionBooksPastTheCeiling`,
`TestReservationAdmissionStopsAtTheCeiling` and
`TestWakeReplacesANodeTheDurableReservationFindsFull`.
