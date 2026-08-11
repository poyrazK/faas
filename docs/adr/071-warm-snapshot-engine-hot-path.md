# ADR-071 · Warm-snapshot engine hot-path

- **Status:** superseded-by: ADR-074 (and the request-count gate realised by ADR-095 C10)
- **Date:** 2026-08-03
- **Issue:** #470 / PR A (extends PR #525 / PR #543)
- **Supersedes:** vmmd-side pause/snapshot/resume plan from PR #525 (the data layer); the engine hot-path was deferred.
- **Decision:** Wire the warm-tier capture into the engine's `Park` site and a plan-gated tier-selection into the wake site. The two captures share one appMu window, the warm capture runs while the runner is still RUNNING (before the PARKED transition), and the failure path destroys the VM without writing the warm row.

## Why

PR #525 shipped the warm-snapshot data layer (`snapshots.tier`, `LatestSnapshotForTier`, plan-gated `WarmSnapshotAllowed`). PR #543 shipped the upstream signal (`instances.framework_ready_at` stamped by vmmd when the guest-init handshake DGRAM arrives). Until A lands, the warm tier is dormant: no engine captures it, no wake consults it. The 350 ms warm-wake budget (§6.3) needs the warm capture to fire on Park and the wake site to prefer warm when the plan allows.

The Firecracker primitive gap makes this non-trivial: there is no `pause-snapshot-resume` primitive. The existing `JailerVMM.Snapshot` is a one-shot atomic pause → `/snapshot/create` → destroy sequence. The warm capture adds a new `Manager.WarmSnapshot` that does NOT release the chroot / cgroup / netns after capture — vmmd does the same pause + `/snapshot/create` + storage publish, then `PATCH /vm {"state":"Resumed"}` brings the runner back to RUNNING. The engine then continues the existing init-tier capture path (which IS the destroy path).

## Capture sequence (single appMu window, ~300–500 ms total)

1. RUNNING instance with `framework_ready_at != NULL` (PR #543 stamp).
2. Engine holds appMu (the existing `Park` lock). `snapshotAndPark` runs the warm capture FIRST while the row is still RUNNING — the state machine's `PARKED → STOPPED` edge is not allowed (`pkg/state/machine.go:61`), so a warm failure must land on the legal `RUNNING → STOPPED` edge before the PARKED transition fires.
   - Warm capture: `vmm.WarmSnapshot` → `snap/<dep>/warm/mem` + `snap/<dep>/warm/vmstate` published → `emitSnapshotWritten{ tier:"warm" }` → vmmd `PATCH /vm Resumed` → runner back to RUNNING.
   - Init capture: `vmm.PauseAndSnapshot` → `snap/<dep>/mem` + `snap/<dep>/vmstate` published → `emitSnapshotWritten{ tier:"init" }`.
   - `transition(STATEParked)`.
   - Both rows are written by imaged's `snapshot_written` subscriber (PR #525) — the engine is sole notifier, imaged is sole writer. No direct `store.CreateSnapshot` call from the engine.
3. **Failure path** (warm capture error):
   - `vmm.Destroy(ins.NodeID, ins.ID)` releases the jailer / cgroup / netns.
   - `ledger.Release(ins.ID)`.
   - `transitionWithKind(STATEStopped, "warm_capture_error", "warm_snapshot_failed")` — RUNNING → STOPPED is a legal edge.
   - `WarmSnapshotErrors("vmm_call").Inc()`.
   - Init capture is **skipped** (the VM is destroyed, so `PauseAndSnapshot` would target a dead process). No init row is written for this Park. PR C's GC sweep evicts the orphaned warm blob (if any). The next wake cold-boots (ADR-005).

## Plan gate (sticky-on-downgrade)

- `pkg/api.Plan.WarmSnapshotAllowed()` is the single source of truth. Cap returns `false` (Free, Hobby).
- `Engine.captureWarmSnapshotLocked` is gated by `app.WarmSnapshotEnabled && acct.Plan.WarmSnapshotAllowed()`. The plan gate is consulted at the Park site too (skip the warm capture round-trip for plans that won't use it).
- `Engine.usableSnapshotForWake(ctx, dep.ID, plan)` is the wake counterpart. Free/Hobby reads `LatestSnapshotForTier(init)` directly. Pro/Scale calls `LatestSnapshot` which already ranks warm > init on `(tier='warm') DESC, created_at DESC` (PR #525).
- **Sticky-on-downgrade** (ADR-070 §Plan gate): `apps.warm_snapshot_enabled` stays `true` across a Pro→Free downgrade. The plan gate at wake time reads the warm row off the disk but ignores it. The next park the plan allows warm again will pick up where the engine left off.

## Per-tier storage keys

- `pkg/state.WarmSnapMemKey(dep) → "snap/<dep>/warm/mem"`.
- `pkg/state.WarmSnapVMStateKey(dep) → "snap/<dep>/warm/vmstate"`.
- The `/warm/` segment keeps blobs physically separate from the init tier so PR C's per-tier GC can keep 2+2 without conflating them.

## Capture gates (any one fails → no warm capture)

| Gate | Source | Failure |
|---|---|---|
| 1. app.WarmSnapshotEnabled | `apps.warm_snapshot_enabled` | silently skip |
| 2. acct.Plan.WarmSnapshotAllowed() | `pkg/api/limits.go` | silently skip |
| 3. ins.FrameworkReadyAt != NULL | PR #543 stamp | silently skip (freshly primed instance, not warm) |
| 4. now - FrameworkReadyAt >= app.WarmSnapshotMinMs | `apps.warm_snapshot_min_ms` | silently skip (not warm long enough) |
| 5. ins.RequestCount >= app.WarmSnapshotMinRequests | `apps.warm_snapshot_min_requests` (ADR-095 C10) | silently skip (served too few requests) |

The fifth gate (request count) is the second half of the warm-snapshot
floor alongside the time-since-first-ready half. Together the two
halves pin the "warm path is the steady-state, cold-boot is the
exception" invariant: a freshly-primed instance, regardless of how
long the runner has been alive, must serve at least N requests before
the engine promotes it to a warm-tier snapshot. ADR-095 C10 closes
this gate; the column lives at `instances.request_count` (migration
00216) and is surfaced on `WakeResult.RequestCount` so the gateway
per-instance cache can render "warming up" vs "warmed" without a
second round-trip.

## Engine invariants preserved

- **m.live[instance] invariant** (PR #543 / ADR-022 §3.2): `Manager.WarmSnapshot` does NOT touch `m.live[instance]` or `cidToID`. The warm chroot / cgroup / netns stay attached after the capture. Only the explicit `Destroy` call on the failure path releases them.
- **appMu window grows from ~100 ms to ~300–500 ms** for one Park. Reaper cadence is 1 s tick (`pkg/sched/loop.go:917`), so this does not deadlock. Verify with `appMu_hold_seconds` log line at the Park entry / exit.
- **Pause freezes the runner** for ~100–300 ms mid-request. Acceptable for the reaper path (no customer is waiting on warm tier; they're waiting on the next wake's restore from the warm blob, dominated by cold-boot savings, not pause).
- **Fail-loud counters** (`vmmd_warm_snapshot_errors_total{reason}`): `vmm_call` on vmm-side failures, `store_write` on schema mismatch / unique violation. Pre-instantiated at boot so dashboard surfaces zero on idle.

## Out of scope (deferred to PR C)

The PR-C list originally enumerated in this section shipped in
ADR-074 (audit kinds, CLI flags, per-tier GC, dashboard panels).
The 5th gate — request-count-based promotion — shipped in ADR-095
C10. The list is kept here as a historical marker; the current
status of each item is documented in ADR-074 and ADR-095.

## Consequences

- Wake latency budget drops for Pro/Scale apps: warm restore is ~10–30 ms vs ~300 ms cold-boot. §6.3's 350 ms goal is now reachable for warm-path restores.
- Disk usage doubles per app: 2 warm + 2 init snapshots per deployment (PR C's GC).
- `AccountRow` plan downgrade semantics: no migration needed. The engine reads the plan at wake time; the warm row stays on disk until the next cold-boot (which lands on the init row) and is GC'd by PR C's per-tier policy.
- Failure semantics: warm capture bugs (disk-full, vmmd abort) destroy the VM. The customer sees a 503 on the next request (cold-boot + restore). The audit row with kind=`warm_capture_error` is the operator's signal.

## Rejected alternatives

- **Pause the VM twice in one Park.** The plan's first draft tried this. The current A.3 draft proves it works (`#store.CreateSnapshot` for warm + audit + snapshot_written), but the Park path's appMu window is already 100 ms; doubling it would push reaper budget to ~500 ms. Capturing warm BEFORE the init transition (the current shape) is cleaner because the warm path is fully independent of the init path.
- **Capture warm AFTER the PARKED transition.** Rejected: the state machine forbids `PARKED → STOPPED` (`pkg/state/machine.go:61`). The warm failure path needs to land in STOPPED, so the row must still be RUNNING when the warm capture fires.
- **Capture warm in a separate background goroutine.** Rejected: it would require its own appMu acquisition, racing with the engine's reaper + Wake paths. The serialized one-window capture is load-bearing for the invariant.
- **Reuse the existing `JailerVMM.Snapshot` for the warm capture.** Rejected: `Snapshot` is a one-shot kill sequence. A second call would fail because the Firecracker process is already gone. The new `SnapshotKeepAlive` + `ResumeVM` helper pair is the only path that keeps the chroot alive.
- **Skip the failure destroy and just log.** Rejected: a paused VM that's stuck in `/snapshot/create` failure has no runner to handle the next request. The next customer request would block on the wake path's waitReady forever. Destroy is the only safe release.

## Critical reference files

| Concern | Path |
|---|---|
| Park core (PR A adds warm capture before PARKED) | `pkg/sched/engine.go` (`snapshotAndPark`) |
| Warm capture helper | `pkg/sched/engine.go` (`captureWarmSnapshotLocked`) |
| Plan-gated wake tier selection | `pkg/sched/engine.go` (`usableSnapshotForWake`) |
| VM lifecycle (Pause + Snapshot + Resume) | `pkg/fcvm/vmm.go` (`SnapshotKeepAlive`, `ResumeVM`) |
| Manager entry (no teardown) | `pkg/fcvm/manager.go` (`Manager.WarmSnapshot`) |
| Wire shape | `api/proto/onebox/faas/vmmd/v1/vmmd.proto` (`WarmSnapshot`) |
| Per-tier keys | `pkg/state/keys.go` (`WarmSnapMemKey`, `WarmSnapVMStateKey`) |
| Tier-aware store contract | `pkg/state/store.go` (`LatestSnapshotForTier`) |
| Plan gate | `pkg/api/limits.go:1437` (`Plan.WarmSnapshotAllowed`) |
| Error counter | `pkg/wire/metrics.go` (`WarmSnapshotErrors`) |
| Sticky-on-downgrade | `cmd/apid/handlers_ext.go:232-256` (already in place) |
