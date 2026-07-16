# Scale-out & Workload Classes — future implementation plan

- **Status:** planning doc (not a decision record). The choices below are *leans*,
  not accepted ADRs. Each one becomes a numbered ADR in [`adr/`](adr/) at the
  milestone/gate that forces it. Nothing here changes v1 (M0–M8); v1 stays
  strictly one-box (spec §14).
- **Scope:** how the platform grows past a single Hetzner EX44, and how a
  long-running **services** tier (Fargate/ECS-shaped) can coexist with the
  **functions** tier (Lambda-shaped) on the same substrate.
- **Source of truth:** this doc must never contradict
  [`faas_implementation_spec.md`](faas_implementation_spec.md) or the financial
  model. Where it proposes something new, it names the ADR that will decide it.

## Why one doc for two topics

They are the same decision viewed twice. The whole "which tenant type gets the
RAM" tension (functions want burst headroom free; services want to pin a floor)
**only exists because it is one box.** Go multi-box and the contention becomes a
placement policy — you dedicate nodes. So the workload-class question and the
scale-out question resolve together, and sequencing one fixes the other.

## What does NOT change (survives every phase)

These are load-bearing invariants (CLAUDE.md, spec §6.2). Scale-out and the
services tier are built *around* them, never through them:

1. Every workload is a jailer'd Firecracker microVM — the isolation boundary is a
   real VM, not a shared kernel.
2. `schedd` is the only writer to `instances`; `apid` the only writer to
   customer-intent; `vmmd` the only root component. Sharding replicates these
   owners **per node**, it does not add second writers.
3. Cold boot always works (ADR-005) — snapshots stay cache, not truth. This is
   what makes de-localizing snapshot storage (Phase 2) safe.
4. Every quota/limit stays in `pkg/api/limits.go`. New workload-class limits go
   there too, never inline.

## Choices (our leans)

| # | Decision | Forcing reason | Becomes ADR at |
|---|---|---|---|
| D1 | **Vertical before horizontal.** First relief for capacity/RAM pressure is a bigger dedicated box, not more boxes. | Near-zero code change (bigger ceiling + more slots); buys quarters of runway. | — (ops choice, no ADR) |
| D2 | **Horizontal = shard apps by node, one `schedd` per node, `apid` routes writes**, plus a new **placement scheduler**. | Spec line 131 already designed the `schedd` interface (`EnsureInstance`/`Park`/`Evict`) narrow for exactly this. | Gate A |
| D3 | **Two "watch-it walls": local snapshot storage and unix-socket transport.** Keep them thin; they are the first things rebuilt at scale-out. | Both are same-host-only assumptions. Everything else already scales out cleanly. | Gate A (folds into D2) |
| D4 | **One platform, two workload classes** (`function`, `service`) — not two platforms. ~80% of the substrate (`vmmd`, `imaged`, `pkg/oci`/`rootfs`, gateway, admission ledger) is shared. | The schema already carries `AppType{app,function}` (`pkg/state/types.go`). Building two engines would duplicate the hardened core. | services milestone (post-M8) |
| D5 | **Services tier ships only after multi-box.** On one box it must fight functions for one RAM budget; on N boxes it is a placement policy. | Removes the zero-sum RAM fight entirely; makes the tier's unit economics tractable. | post Gate A |
| D6 | **The snapshot moat is functions-only.** Services cold-boot/restore per replica; price them accordingly. | N-instances-from-one-snapshot depends on the identical inner network world (ADR-009), which a multi-replica, addressable service gives up. | services milestone |

## What actually changes going one-box → multi-box

Grounded in today's wiring. Left column is a same-host assumption; right column
is the multi-host replacement (all Gate A, per D2/D3):

| Today (one box) | Multi-box needs |
|---|---|
| gRPC over unix sockets in `/run/faas/` (vmmd/schedd) | TCP behind mTLS — the re-eval trigger already noted in ADR-013/015/018 |
| Snapshots on local `/srv/fc` | wake-on-node-B needs node-A's snapshot → **sticky placement** (route an app to its warm node) or shared/replicated snapshot store. Cold-boot fallback (ADR-005) is the safety net that makes this non-fatal. |
| One in-process admission ledger, one 47,600 MB budget (`pkg/sched/admission.go`) | ledger **per node** + a **placement scheduler** deciding *which node* admits a VM. This is the one genuinely new component. |
| `gatewayd` = the only public listener | an edge/routing tier that knows which node an app is warm on (or route-anywhere + wake). |
| One `schedd`, sole writer to `instances` | one `schedd` per node; apps sharded by node; `apid` routes writes to the owning shard (spec line 131). |

Everything else — per-slot resource derivation (`pkg/fcvm`), Postgres/sqlc,
`pkg/api/limits.go`, the state machine — is already node-agnostic.

## Workload classes: where functions and services diverge

Shared: the microVM lifecycle, OCI→ext4 ingestion, the router core, the RAM/vCPU
accounting. Divergent (this is the whole services-tier build):

| Axis | `function` (Lambda) | `service` (Fargate/ECS) |
|---|---|---|
| Lifecycle | park→wake, scale-to-zero, snapshot restore | `desired_count` reconciler, min-replicas, no PARKED (or opt-in scale-to-zero) |
| Scheduler | wake gate + idle reaper (`pkg/sched/reaper.go`) | **reconcile loop** (desired vs actual), health checks, rolling deploy |
| Reaper | idle-reap after 30–600 s | **exempt** from idle reap; min-instances pin RAM |
| Networking | identical inner world → N-from-1 snapshot reuse | stable per-replica identity, LB fan-out; snapshot reuse mostly N/A |
| Billing | GB-h per running-second, €0 when parked | per-second **while up**, continuous floor |

Two concrete code touch-points when the tier lands: `SelectEvictions`/`ReapIdle`
grow a workload-class guard so a service is never parked, and `schedd` grows a
**reconciler** beside the wake/park engine — that reconciler *is* the "ECS" piece.

## Phased plan (tied to existing milestones/gates)

**Phase 0 — now → M8 (single-box v1).**
Do nothing for scale. Finish serverless. The only discipline: keep the two
watch-it walls (D3) thin — don't pour extra concrete around local snapshot paths
or assume unix-socket locality in new code beyond what vmmd/schedd already do.

**Phase 1 — first customers (vertical).**
Move to a bigger dedicated box when RAM/CPU pressure appears. Config + ledger
ceiling change, no architecture change. Ceiling is real: the biggest box Hetzner
rents, and — the actual forcing function — **one box is one failure domain**
(every customer dies together). Blast radius, not RAM, is what ends Phase 1.

**Phase 2 — Gate A (horizontal + HA).**
Matches spec: M8 ships the "2nd box active-passive" runbook; Gate A is the
FSN+HEL regional pair (§16). Build order:
1. De-local snapshots (sticky placement first; shared store only if needed).
2. Sockets → mTLS TCP (ADR-013/015/018 re-eval triggers).
3. Per-node `schedd`, apps sharded by node, `apid` routes writes.
4. The **placement scheduler** (new component; "which node admits this VM").
Write ADRs D2/D3 here.

**Phase 3 — services / Fargate workload class (post Gate A).**
Now that capacity is partitionable across nodes, add the `service` class:
reconciler, reaper exemption, per-second billing mode, per-replica networking.
Dedicate nodes by class instead of fighting one budget. Write ADRs D4/D5/D6.
**Gate:** a financial-model addendum proving functions + services co-exist
profitably (RAM partition per node) must land *before* implementation — this is a
spreadsheet decision first, code second.

## ADRs this spawns (write at the gate, not now)

- **Gate A:** "Scale-out staging: vertical-first, then per-node sharding +
  placement scheduler" (D2/D3); "Snapshot storage de-localization" (D3);
  "Control-plane transport: unix socket → mTLS TCP" (ADR-013/015/018 successors).
- **Post Gate A:** "`service` workload class: reconciler + reaper carve-out"
  (D4); "Workload-class RAM partition + billing mode" (D5, with financial-model
  addendum); "Services networking & pricing without snapshot reuse" (D6).

## Re-evaluation triggers

- **Any workload that can't park** (long-lived connections, WebSocket/streaming
  — itself a §16 open question) is an early signal the `service` class is needed
  before Gate A; if so, D5's "multi-box first" ordering must be revisited, and
  the one-box RAM partition designed explicitly.
- **A customer needs an arbitrary OCI image** (not built FROM our base): trips the
  two-drive `FROM`-base constraint (`pkg/oci/image.go`). Requires content-addressed
  base sharing — a separate ADR, orthogonal to this plan.
