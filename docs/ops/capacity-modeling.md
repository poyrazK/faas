# Capacity modeling — multi-host extrapolation

Issue #297 acceptance item 2. Anchors the per-node ceiling semantics
that schedd's `NodeLedger` enforces (spec §6.2-2 re-stated per-node),
then walks the measured numbers from the reference staging node and the
linearisation assumptions for 1k / 10k customers across multiple
compute nodes.

This doc is **honest about measured vs. projected**. The historical
file at commit `ab0ab162` was "single-host density + linear
extrapolate" and was explicitly rejected as a basis: it was
guess-based. Every row here is a citation or a `(projected)` mark
with the linearisation spelled out.

## §1 Per-node ceiling semantics (ground truth)

**This is the load-bearing block. Read it before any number below.**

The `RAMAdmissionCeilingMB = 47,600 MB` constant in
`pkg/api/limits.go:499-501` is **per-box**, NOT per-cluster. The
ledger's `Admit` enforces
`node.residentRAM + r.admissionMB() > node.AdmissionCeilingMB` for
the chosen node (see `pkg/sched/admission.go:165-168`). Multi-host
scales linearly: **cluster total admitted RAM is
`Σ(node.AdmissionCeilingMB)`**, not capped at the global 47,600 MB
constant. The `HeadroomMB` doc comment at `admission.go:302-323`
makes this explicit ("Global headroom is sum(ceiling - resident)
across nodes").

The vCPU budget is likewise per node: `compute_nodes.vcpu_budget` is enforced
by `NodeLedger.Admit` for the chosen node. Unregistered or legacy rows fall
back to `VCPUSlots = 160` (`pkg/api/limits.go`), which preserves the original
single-box behavior. Cluster capacity is the sum of the registered node
budgets, not one global 160-vCPU gate.

**The financial model reads `Σ(node.AdmissionCeilingMB)`, not the
global 47,600 MB cap.** A reviewer reading the financial model spreadsheet
and this doc together must see the same numbers; if they see 47,600 MB
on a multi-node fleet, that's a bug.

## §2 Hard limits (source of truth: `pkg/api/limits.go`)

| Constant                      | Value       | Where                              |
|-------------------------------|-------------|------------------------------------|
| `RAMAdmissionCeilingMB`       | 47,600 MB   | per-box (legacy single-box posture)|
| `VCPUSlots`                   | 160         | fallback per-node budget (single-box default) |
| `PerVMOverheadMB`             | 8 MB        | added to every instance's `ram_mb` |
| `FleetSnapshotAvgTargetMB`    | 130 MB      | business metric; alert > 160, page > 200 (spec §12) |
| `BillableRAMMB(ram_mb)`       | `ram_mb + 8`| admission + billing helper         |

The per-app plan maxima (Free / Hobby / Pro / Scale) live in
`pkg/api/limits.go` and are referenced by the property test
universe in `pkg/sched/ledger_property_test.go:37-42`.

## §3 Headline numbers — measured on the reference staging node

> **⚠️ The numbers below are placeholders pending measurement.** This
> doc was drafted before the measurement runs that issue #297
> acceptance item 2 requires. Each row is annotated
> `(measurement pending)` with a marker pointing at the next
> benchmark slice (which becomes V11 in `docs/faas_implementation_spec.md`
> Appendix D). The intent is to **publish the doc skeleton now**
> so the runbook (Phase D) and the property test (Phase E) can
> cross-link against a stable URL; the cells fill in as the
> benchmark slices land.

| Metric                            | 0 customers | 100 customers | 500 customers | 1k customers |
|-----------------------------------|-------------|---------------|---------------|--------------|
| **Wake path RPS** (gateway)       | (pending)   | (pending)     | (pending)     | (pending)    |
| **Wake path RPS** (schedd handler)| (pending)   | (pending)     | (pending)     | (pending)    |
| **Wake path RPS** (vmmd control)  | (pending)   | (pending)     | (pending)     | (pending)    |
| **Live RAM residency (MB)**       | 0           | (pending)     | (pending)     | (pending)    |
| **vCPU busy**                     | 0           | (pending)     | (pending)     | (pending)    |
| **Snapshot count**                | 0           | (pending)     | (pending)     | (pending)    |
| **Snapshot fleet avg MB**         | n/a         | (pending)     | (pending)     | (pending)    |
| **Snapshot restore p50 (ms)**     | n/a         | (pending)     | (pending)     | (pending)    |
| **Snapshot restore p95 (ms)**     | n/a         | (pending)     | (pending)     | (pending)    |

### How each row will be measured

The benchmark slice (V11 in Appendix D) instruments the existing
load-test harness in `cmd/e2e/loadtest/` to:

- **Wake path RPS** — drive a steady-state request rate against
  `https://apps.<domain>/` for a small fixture app, measure
  `gateway_wake_latency_seconds` p50/p95 per second. The
  Prometheus metric is shipped via `pkg/wire.DefaultOpsMetrics`.
  Each box's metric is scraped by its own gatewayd-public +
  gatewayd-internal pair;
  cluster-total is `Σ(gateway_wake_latency_seconds_count)`.
- **Live RAM residency** — `pkg/sched.NodeLedger.ResidentRAM()` is
  the authoritative read; `ResidentRAMForNode(nodeID)` returns the
  per-node value. Cross-checked against
  `SELECT SUM(ram_mb + 8) FROM instances WHERE state IN ('WAKING','COLD_BOOTING','RUNNING')`.
- **Snapshot count + fleet avg MB** —
  `SELECT COUNT(*), AVG(byte_size) FROM snapshots` joins against
  the spec §12 alert `snapshot_fleet_avg_mb`.
- **Snapshot restore latency** — `pkg/fcvm/logbuf` carries
  per-restore timestamps; the load-test harness records
  p50/p95 across N restores. Spec §4.5 target: ≤ 350 ms cold
  boot from snapshot.

The measurement harness is gated behind the existing
`make test-load` target. The results populate the table above and
are referenced from the runbook (Phase D) and the property test
(Phase E) acceptance criteria.

## §4 Multi-host extrapolation (caveated)

This section projects the §3 numbers across multi-host fleets. Every
row marked `(projected)` carries the linearisation assumption
explicitly. **No measurement exists yet for multi-host.** The
extrapolations are sanity checks against the financial model's
revenue-at-N-box assumption, not a load-tested prediction.

### Cluster total admitted RAM

`(projected)` Cluster total admitted RAM at N boxes, each with the
default-local ceiling:

```
Σ(node.AdmissionCeilingMB) = N × 47,600 MB
```

| Boxes | Cluster ceiling | Use case (projected)                              |
|-------|-----------------|--------------------------------------------------|
| 1     | 47,600 MB       | Single-node, reference host (default-local)        |
| 2     | 95,200 MB       | First cut-over (Phase D runbook, staging only)   |
| 4     | 190,400 MB      | Capacity for ~5k customers (linear, projected)   |
| 10    | 476,000 MB      | Capacity for ~10k customers (linear, projected)  |

**Linearisation caveat.** The assumption is each box carries the
default-local 47,600 MB ceiling. A heterogeneous fleet (mix of
24 GB and 56 GB boxes) reduces the cluster ceiling proportionally.
Operators tune per-node via
`compute_nodes.admission_ceiling_mb` (migration 00024), and the
ledger honours the per-row value (`pkg/sched/admission.go:156-159`).

### Cluster total vCPU budget

`(projected)` The vCPU budget is global (not per-node) until
`PerNodeVCPUSlots` lands:

```
max(Σ vCPU used over all nodes) ≤ VCPUSlots = 160   (cluster-wide today)
```

A heterogeneous fleet shares the same 160-slot pool. The
`NodeLedger.UsedVCPU()` aggregate returns the global sum
(`admission.go:333-337`). A node holding more than its share
cannot escape the global budget because the cap is on the sum,
not per-node.

### Per-app concurrency stays global

§6.2-1 is per-app, not per-node. A customer's Scale app with
`max_concurrency=20` cannot run 20 on node A and another 20 on
node B; the cap is 20 cluster-wide. The ledger's `perApp` map
is global (`pkg/sched/admission.go:39`), and the property test
in Phase E pins this under multi-node.

### Wake path RPS scaling

`(projected)` Wake path RPS scales linearly with the number of
gatewayd-internal instances behind the upstream Caddy/Cloudflare edge (with
gatewayd-public as the plain-HTTP ingress), NOT with compute nodes.
A compute node does not increase wake RPS; it increases the
admitted-RAM and vCPU budgets. The wake-path bottleneck today is
gatewayd-internal's listener (spec §6.4 "WakeResponse reverts" row); a
2-gatewayd-internal fleet would double the wake RPS, a 2-compute-node
fleet does not.

### Snapshot locality

`(blocked)` Per-app layer snapshots are stored locally under
`/var/lib/faas/apps/<app_id>/` today (`pkg/fcvm/manager.go`). On
a multi-host fleet, a cold-boot on compute-01 cannot reach the
snapshot on compute-02 without Tier 1 Phase 3
(`OCIRegistryStorageBackend` end-to-end, issue #95 slice 3).
Until Phase 3 ships, **every compute node must hold a local copy
of every app's per-app layer**, defeating the per-host storage
economics (§4.6 two-drive). The runbook (Phase D) calls this out
in its Pre-conditions block.

## §5 Gaps — what we did NOT measure

These are open gaps the load-test harness can't currently
exercise. Each is a follow-up issue (or `(blocked)` against an
open Tier 1 / Tier 2 PR):

- **(blocked)** Snapshot restore latency under multi-host where
  the per-app layer is fetched from a remote (Tier 1 Phase 3).
- **(blocked)** Cross-node admission race — what happens when
  schedd's `choosePlacementLocked` picks node A, but node A
  fails between the pick and the vmmd RPC. The ledger is single-
  leader, so there is no distributed race; but the placement-
  retry behaviour under partial failure is not measured.
- **(open)** Per-node vCPU budget enforcement (today: global
  `VCPUSlots` cap). Issue: filed as a Tier 2 follow-up.
- **(open)** Compute node startup cold-path latency (network
  reachability + capacity report freshness + first admit
  latency). The first 30s of a node's life are unmeasured.
- **(open)** `node_capacity_reports` audit table (ADR-025 §4.5
  follow-up). Today's drops are logged but not queryable.
- **(open)** Wake-path RPS at the cluster level — a
  load-balanced gatewayd-internal + gatewayd-public fleet has never
  been measured at multi-host scale.

## §6 Acceptance against issue #297

The acceptance checklist for issue #297 acceptance item 2:

- [ ] **100 customers measured** — Wake path RPS, RAM residency,
      snapshot count, restore p50/p95. **Status:** measurement
      pending (V11 benchmark slice not yet run).
- [ ] **1k customers measured** — same metrics at 1k. **Status:**
      measurement pending.
- [ ] **10k customers projected** — linearised from 1k per §4.
      **Status:** projection in §4 (caveated).
- [ ] **Per-node ceiling semantics** documented so a finance
      reviewer doesn't mis-read the financial model. **Status:**
      shipped in §1.
- [ ] **Multi-host gaps** enumerated. **Status:** shipped in §5.

## §7 Related docs

- `docs/adr/025-decoupled-control-plane-and-compute.md` v1.1 —
  the Tier 2 pre-requisites callout (issue #297). PR #450.
- `docs/runbooks/multi-host-rollout.md` — Phase D runbook that
  walks the cut-over. PR-pending.
- `pkg/sched/ledger_property_test.go` — Phase E multi-host
  property test extension. PR-pending.
- `docs/scale_out_and_workload_classes.md` — sibling. Workload-
  class-specific content stays there; multi-box capacity-
  specific content lives here.
- `docs/faas_implementation_spec.md` §6.4 — failure-mode
  catalogue (PR #450).
- The financial model spreadsheet — the revenue model. Read §1
  first if the numbers look off.
