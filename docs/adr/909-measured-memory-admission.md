# ADR-909: Admission by measured memory

- Status: Proposed. It changes invariant 2 and the node capacity the
  financial model assumes, so it needs an owner decision first.
- Date: 2026-10-12
- Related: ADR-906, ADR-907, ADR-193, spec §1, §4.7, §6.2 invariant 2
- Would amend: spec §6.2 invariant 2

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

Not built yet, and why the flag stays off:

- The charge is the latest sample plus 25% headroom, not the 10-minute p95
  of proposal 1. A VM that grows right after a sample is covered only by
  the headroom and the plan cap.
- Eviction under memory pressure (proposal 3) does not exist. Without it,
  a burst past the measured charges falls to the kernel OOM killer.
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
