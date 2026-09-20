# ADR-191 · Scheduler divergence reconciliation and bounded loop dispatch

- **Status:** accepted
- **Date:** 2026-09-21
- **Decision:** (1) Reconcile `instances` rows against what the owning vmmd
  actually reports, shipped report-only behind `FAAS_SCHEDD_RECONCILE_ENFORCE`.
  (2) Move every long-running notification handler onto one bounded, coalescing
  work pool, and cut schedd's main-loop stall budget from 180 s to 60 s.
- **Why:** ADR-190 made a wedged daemon restart itself, but both scheduler-side
  causes behind it remain. Rows and reality drift apart with no owner for the
  "row says RUNNING, VM is gone" direction, and the notification loop still
  shares one goroutine with eighteen tickers.
- **Consequences:** new metrics `schedd_instance_divergence_total{outcome}`,
  `schedd_loop_work_total{kind,outcome}`,
  `schedd_loop_work_duration_seconds{kind}`; new alert `FaasInstanceDivergence`
  (warn) with runbook; new env `FAAS_SCHEDD_RECONCILE_ENFORCE`; schedd
  `WatchdogSec` 180s → 120s. No migrations, no proto changes.
- **Rejected alternatives:** listed per decision.

## Note on the ADR number

`docs/adr/190-production-buildkit-cache.md` and
`docs/adr/190-daemon-durability-primitives.md` both exist: two PRs picked 190
concurrently and the BuildKit one merged first. The repository already carries
several duplicate numbers (157, 158, 167, 168), so this ADR takes 191 rather
than renaming 67 references across the tree for a cosmetic fix. Worth a
dedicated cleanup PR that renumbers and adds a CI gate, not worth doing here.

## 1. Instance divergence reconciliation

### Context

`instances` rows and running Firecracker processes drift. vmmd owns one
direction: `fcvm.ReapOrphanedJails` tears down VMs with no live row on vmmd
boot. A production node on 2026-09-04 had 23 such VMs, the oldest 3.7 days,
5.3 GB of tenant RAM.

The other direction had no owner. A row that says RUNNING while the VM is gone
keeps billing the customer at plan RAM + 8 MB per second (§4.7), keeps its
admission slot reserved against the 47,600 MB ceiling, and keeps receiving
routed requests that fail. `Engine.ReconcileDeadNodeInstances` covers only a
whole node going silent (`compute_nodes.last_heartbeat_at` older than
`DeadNodeReconcilerStalenessSeconds`); a single VM dying under a healthy node —
an OOM kill outside the liveness path, an operator's `kill -9`, a destroy RPC
that failed after the VM died — is invisible to it.

### Decision

`InstanceDivergenceReconciler` (`pkg/sched/instance_divergence.go`) sweeps every
`InstanceDivergenceIntervalSeconds` (30 s) for live rows the owning vmmd is not
reporting.

**The signal already exists.** vmmd streams per-instance capacity telemetry to
schedd continuously; it lands in `NodeTelemetryCache` and the instance-stats
poller projects it into the reader behind `InstanceActivityReader`, which
already drops stale samples ("Stale samples are absent, not zero"). A key
present in `SnapshotActivity` means the owning vmmd reported that VM recently.
The reconciler consumes what is already flowing: no new RPC, no proto change,
no second `Stats` call per node.

**Four gates before a row counts**, each closing a specific false-positive path:

| Gate | Closes |
|---|---|
| The node must have reported ≥1 of its own instances | A silent node is the dead-node reconciler's job; the two sweeps take disjoint inputs and cannot act on one row |
| Instance older than `InstanceDivergenceGraceSeconds` (60 s) | A just-admitted VM missing from the last telemetry batch |
| Absent on two consecutive sweeps | One dropped telemetry batch parking a live app |
| An empty snapshot resets all candidates | A telemetry outage accumulating candidates that all fire when it returns |

Plus `InstanceDivergenceTickLimit` (50) on the per-tick write burst, the same
bound and reasoning as `DeadNodeReconcilerTickLimit`.

**Report-only by default.** `FAAS_SCHEDD_RECONCILE_ENFORCE=1` is required before
any row is written; otherwise the sweep increments
`schedd_instance_divergence_total{outcome="suppressed"}` and logs what it would
have repaired. A bug in this sweep parks healthy apps fleet-wide, so the
divergence counter gets read against production reality for a week before
enforcement is flipped in a one-line follow-up.

**FAILED, not PARKED**, under enforcement, for the reason
`ReconcileDeadNodeInstances` already records: no snapshot was taken because the
VM is gone, and claiming PARKED asserts a snapshot that does not exist. FAILED
is cold-bootable (ADR-005: snapshots are cache, not truth), so the customer's
next request still serves — it just pays the cold-boot path. The transition
goes through `UpdateInstanceStateIf` so a peer that already parked, evicted or
migrated the row wins, and `Ledger().Release` runs on the conflict path too
because it is idempotent and closes the billing side of the same race.

### Known gap

A node whose *only* instance dies reports nothing, which is indistinguishable
from a silent node, so it is skipped. That row stays live until the node's own
heartbeat fails or the next wake reconciles it. Closing this needs a vmmd-side
"I am up and I have zero VMs" assertion — a separate change, deliberately not
bundled here.

### Rejected alternatives

- **A `ListInstances` RPC on vmmd.** The telemetry stream already carries the
  set; a second source would drift from the first and add a proto change plus a
  per-node call every 30 s.
- **Acting on the first absent sweep.** Telemetry is best-effort and batched.
  One dropped batch would park live customer apps, which is a worse outcome than
  30 s of extra billing on a dead VM.
- **Destroying VMs with no row from schedd** (the reverse direction). Schedd
  would be ordering a kill from a possibly-stale database read; the failure mode
  is killing a customer's running app. That direction stays in vmmd, where the
  durable gate already lives.
- **Enforcing from day one.** Closes the billing leak a week earlier at the cost
  of a fleet-wide false-positive risk with no production evidence behind it.

## 2. Bounded loop dispatch

### Context

`Loop.Run`'s select goroutine owns the pg_notify channel and eighteen tickers.
Anything a handler arm does synchronously delays the reaper, the §6.1 watchdog,
cron and heartbeat. On 2026-09-03 a deadline-less `PauseAndSnapshot` held that
goroutine for 10+ minutes; schedd stayed `active`, answered `/metrics` in 30 ms,
and did no work at all.

`dispatchPrime` escaped with a bounded four-slot pool. Four other arms escaped
with a bare `go func`, which removes the head-of-line blocking but replaces it
with unbounded goroutine growth: a burst of `app_changed` spawns one goroutine
per notification, each taking the same per-app engine lock, against a
16-connection Postgres pool.

### Decision

One `workPool` (`pkg/sched/loopwork.go`) for all five kinds, with per-kind slot
budgets and a per-kind coalescing key that generalises the prime single-flight.

The overflow policy is per kind because the right answer differs:

- **`prime` overflows to inline.** A dropped `snapshot_prime` strands the
  deployment in `snapshotting` with nothing to retry it — the notification is
  consumed and gone. Blocking the loop stays the lesser harm, bounded by
  `SnapshotTimeout`. Behaviour is unchanged from before this ADR.
- **The four reconcile kinds overflow to drop.** Each is idempotent over a
  durable table with a safety ticker behind it, so a dropped duplicate costs one
  tick of latency. Dropping beats unbounded goroutines.

A panicking task is recovered, counted and its slot and key released: the loop
is the only thing keeping this node scheduling.

`handleAppWake` and the two private-network arms stay **synchronous on purpose**.
Each returns an error that decides whether the durable notification is
acknowledged; moving them off-loop means re-plumbing outbox ack through the
worker, which is a separate change.

`MainLoopBudget` drops 180 s → 60 s, and schedd's `WatchdogSec` 180 s → 120 s to
keep the ADR-190 invariant that the watchdog outlasts twice the loop budget. It
cannot go below 60 s while `handleAppWake` is inline: `EnsureWake` can
legitimately cold-boot at `ColdBootTimeout` (35 s) plus admission.

### Rejected alternatives

- **One global worker pool.** A burst of cheap app reconciles would starve the
  prime path, which is the one kind that cannot be dropped.
- **Unbounded `go func` everywhere** (the status quo for four arms). Bounded
  latency, unbounded resource use; the connection pool is the real limit and
  nothing was respecting it.
- **Moving every arm off-loop, including the durable ones.** Needs outbox ack
  plumbed through the worker. Worth doing, not worth coupling to this change.
