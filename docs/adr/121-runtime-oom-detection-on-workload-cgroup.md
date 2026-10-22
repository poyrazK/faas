# ADR-121 · Runtime OOM detection on the per-workload cgroup

- **Status:** **Proposed**
- **Date:** 2026-08-20
- **Decision:** The guest-init static PID-1 places a
  `cgroup.events` listener on the per-workload cgroup v2 leaf
  (ADR-069) for every runtime VM. On the first `oom_kill`
  increment, the listener samples the leaf's high-watermark
  (`memory.events.high`) plus the leaf's cap (`memory.max`),
  frames them as a vsock DGRAM type=0x05 on port 1027, and
  the host's `cmd/vmmd` dispatches the tuple through
  `Manager.ReportWorkloadOOM` → schedd
  `Engine.DestroyForWorkloadOOMFailure` →
  `whycopy.CodeAppRuntimeOOM` (templated peak MB + plan cap).
  The end-to-end stamp replaces today's silent
  `CodeDeployFailed` collapse for the customer-visible OOM
  failure class.

## Context

PR #987 (error-explanations mega, 2026-08-19) shipped the
catalog, wire shape, DB persistence, and customer-facing
surfaces. Cluster A (PR #998, 2026-08-20) closed the
runbook gaps for the existing catalog. The audit moved
1.5/9 → 8/9 → 9/9 of named deployment failures having
structured what/why/fix/log.

**One gap remained — the longest-standing one.** The
`app_runtime_oom` code (`pkg/api/errors.go:1122` +
constructor at `pkg/api/errors.go:1924-1936`) and the
catalog row (`pkg/whycopy/whycopy.go:118-123`) both ship.
But **the detection site has no caller.** The
constructor's doc-comment names a "guest/init/
cgroup_partition_linux.go::cgroup.events listener" as
the producer, but that listener did not exist. When a
customer workload hits the plan RAM cap in production,
the failure did NOT stamp `app_runtime_oom` — it
collapsed to the generic `CodeDeployFailed` and the
customer saw nothing actionable.

The legacy `state.FailureOOM = "oom"` path was
build-VM-only, consumed by builderd for the same
classification. There was no runtime side.

## Why guest-init listener (not host-side)

The host's per-VM cgroup scope (vmmd's `writePlanCgroup`
at `pkg/fcvm/cgroup.go`) scopes the firecracker process
from the outside. Inside the guest, processes are in the
guest kernel's memcg tree, and the host's cgroup
hierarchy is not visible (the guest cgroup namespace is
isolated by `guest-init`'s `CLONE_NEWCGROUP` step in
`main_linux.go`). A workload that exceeds its plan RAM
inside the guest triggers the guest's per-leaf memory
controller, not the host's per-VM scope. The host can
only see the firecracker process — it cannot see per-PID
OOM kills inside the VM.

The only place to detect a per-workload OOM in-VM is
guest-init, on the leaf where the workload PIDs live.

## Why a new DGRAM type (not existing 0x01..0x04)

The framework-ready channel (port 1027, DGRAM, type
0x01..0x04 closed set) already carries guest-init →
host events. Adding type 0x05 to the closed set:

  - **Reuses the existing dispatcher** in
    `cmd/vmmd/framework_ready_recv.go` — a single switch
    arm, no new listener, no new vsock port.
  - **Reuses the existing peer-CID → instance-id
    resolution** — the host resolves instance identity
    from the DGRAM peer CID, the same join the other
    four types use.
  - **Stays inside the closed-set discipline** — types
    0x01..0x05 are owned by a single, co-located
    dispatcher with a tripwire (`TestFrameworkReady_
    TypeClosedSet`) that pins the 5-value set. Future
    event classes add a byte + a switch case + a test in
    lockstep.

The alternative (extending `LivenessFailedReport` with
an optional reason enum) was rejected: the message
shape is wrong (`reason` is a closed string; the OOM
case needs a `(peakMB, planMB int)` tuple). ADR-016
says extend-proto-when-shape-changes.

## Why a new schedd RPC + Engine function

The schedd's `Engine.DestroyForLivenessFailure(reason)`
is the existing vmmd-triggered destroy path (issue
#554, ADR-078). It is gated on a single string `reason`
that flows into the audit log. Adding a workload-OOM
case as a new `reason` would:

  - **Conflate two failure classes.** Liveness failure
    is a probe-and-counter outcome; OOM is a cgroup
    controller outcome. Different blast radius
    (liveness: same app on the next probe cycle; OOM:
    cold-boot needed because the workload literally
    can't fit).
  - **Bolt an observed-payload field onto a closed-set
    reason string.** The reason would have to carry
    `(peak, plan)`, which is what protobuf is for.

The Cluster C shape is:

  - **New gRPC RPC** `ReportWorkloadOOM(instance_id,
    peak_mb, plan_mb)` — fields are additive per
    ADR-016.
  - **New `SchedAPI.DestroyForWorkloadOOMFailure(ctx,
    instanceID, peakMB, planMB)`** — mirrors
    `DestroyForLivenessFailure` exactly, with a typed
    observed payload.
  - **New whycopy `Observed` closure** for
    `CodeAppRuntimeOOM` — templated text, not a
    classification surface.

## Decision

### Wire envelope

```
[1B type=0x05][json:{"peak_mb":N,"plan_mb":N}]
```

Body cap: `VsockWorkloadOOMMaxBody uint32 = 256` bytes
(host-side). The actual payload is < 32 bytes; 256 is a
generous future-proof margin. The bound is mirrored on
the guest init side at `workloadOOMEmitMaxBody`.

### Detection seam

`guest/init/cgroup_partition_linux.go::WatchOOM(ctx,
leaf, planMB, emit, log)` — exported listener that:

  1. Opens the leaf's `cgroup.events` file (O_RDONLY +
     O_NONBLOCK).
  2. Reads the baseline `oom_kill` counter
     (memory.events file).
  3. Loops on `poll(2)` with `POLLPRI` for
     `cgroup.events` updates (1s timeout keeps ctx
     cancel responsive).
  4. On each wakeup, re-reads `memory.events` for the
     `oom_kill` counter delta.
  5. When delta > 0: samples `memory.events.high` (the
     leaf's high-watermark since last reset), falls
     back to `memory.current` (live usage) if absent,
     re-reads `memory.max` for the cap if `planMB` is 0
     (legacy / unknown plan), invokes the emit
     callback, returns.

The listener exits on first fire (the workload is dead
when an oom_kill lands) and on ctx cancel (VM
shutdown).

### Wire helper

`guest/init/framework_ready_emit.go::EmitWorkloadOOM
(ctx, peakMB, planMB)` — frames the JSON envelope, opens
a fresh AF_VSOCK DGRAM socket per call (1s send timeout
floor), sends to
`VMADDR_CID_HOST:VsockFrameworkReadyPort`. Best-effort:
the listener exits on first fire regardless of send
result; a missed signal just means the customer sees
the deployment "succeeded then failed" rather than
"fail-immediate".

### Host dispatch

`cmd/vmmd/framework_ready_recv.go::dispatchWorkloadOOM
(instance, wire)` — extends the existing closed-set
switch with a `parseFWReadyKindWorkloadOOM` case. Calls
`r.mgr.ReportWorkloadOOM(ctx, instance, wire.PeakMB,
wire.PlanMB)` (the Manager mirror of the existing
liveness path).

### Manager sink

`pkg/fcvm/manager.go`:

  - `WorkloadOOMSink func(ctx, instanceID, peakMB,
    planMB int)` — mirrors `LivenessFailedSink`.
  - `WithWorkloadOOMSink(s WorkloadOOMSink)` option.
  - `Manager.ReportWorkloadOOM(ctx, instanceID, peakMB,
    planMB)` method — nil-safe (local-dev vmmd without
    schedd gets a no-op).

### vmmd relay

`cmd/vmmd/main.go` — wires the
`Manager.WithWorkloadOOMSink` closure to the schedd
gRPC RPC over the same `deps.scheddTarget` +
`deps.scheddClientTLS` channel the liveness relay uses.
Bounded by `ReportWorkloadOOMCtxTimeout = 3s`.

Skipped on the single-box default-local path
(`deps.scheddTarget == ""`): mirrors the liveness
relay gating. The framework_ready receiver still
parses + dispatch type=0x05 DGRAMs (the type
validation is host-local), but the sink is a no-op.

### Sched stamping

`pkg/sched/engine.go::Engine.DestroyForWorkloadOOMFailure
(ctx, instanceID, peakMB, planMB)` — mirrors
`DestroyForLivenessFailure` exactly:

  1. Reads `InstanceByID` (sanity check).
  2. Locks `appMu`, re-reads under lock, reads
     `instance.DeploymentID`.
  3. **Skips if state is not `state.StateRunning`** —
     idempotency guard against duplicate relay races.
  4. Marks warm + init snapshots stale (ADR-005
     invariant — cold-boot must always work, so a
     workload that OOM'd at plan cap may need a
     fresh snapshot).
  5. `timedDestroy` (the existing destructure path).
  6. Emits `events.WorkloadOOMFailed{...}` audit row.
  7. Transitions `RUNNING → STOPPED` with
     `kind="workload_oom_failed"`.
  8. Increments `e.ops.WorkloadOOMKills` (new counter
     — `vmm_workload_oom_kills_total{app, deployment}`).
  9. **Always stamps:**
     - `problem := &api.Problem{Code: api.CodeAppRuntimeOOM, Status: 422, ...}`
     - `_ = whycopy.Decorate(problem, api.CodeAppRuntimeOOM, struct{PeakMB, PlanMB int}{peakMB, planMB})`
     - `_ = e.store.SetDeploymentFailedEx(ctx, deploymentID, api.CodeAppRuntimeOOM, ...problem.Hint, problem.why, problem.Fix, nil)`

### Whycopy Observed closure

`pkg/whycopy/whycopy.go::CodeAppRuntimeOOM.Observed`
closure templates the (peakMB, planMB) tuple into
`Why` ("the cgroup memory controller killed the
process at <N> MB (plan cap <M> MB + 8 MB
overhead)…") and `Fix` ("• upgrade from a <M> MB
plan to a plan with ≥ <N+8> MB of RAM\n• trim
in-memory state…"). The static `Why` / `Fix` strings
remain the fallback when `Decorate` is called with
`observed=nil` (no templating — the existing 6
customer fixtures see the right prose).

The cross-reference line "if this is a build step,
see /errors/build/limits#memory instead" was dropped
— the build-OOM path is `CodeBuildOOM`, not
`CodeAppRuntimeOOM`; the cross-reference was
misleading because the two constructors are
distinct.

### Closed-set tripwire

`cmd/vmmd/framework_ready_recv.go`'s existing
closed-set switch extended to 5-value set
({0x01, 0x02, 0x03, 0x04, 0x05}). A new test
`TestParseFrameworkReadyDatagram_TypeClosedSet` pins
the discipline: future event classes (0x06+) need a
byte + a switch case + a test in lockstep.

## Files modified

### Wire envelope
- `cmd/vmmd/framework_ready_recv.go` — closed-set
  extended, `parseFWReadyKindWorkloadOOM` enum +
  switch case, `parseFWReadyMsg.WorkloadOOM` field,
  `workloadOOMWire` body struct,
  `dispatchWorkloadOOM` function.
- `cmd/vmmd/framework_ready_recv_test.go` — existing
  closed-set test updated to scan to byte 0x06.
- `cmd/vmmd/framework_ready_workload_oom_test.go` —
  new file: 5 unit tests (valid body, malformed JSON,
  closed-set guard, observed payload, TypeLabel).

### Guest-init
- `guest/init/cgroup_partition_linux.go` — new
  `WatchOOM` function, `workloadOOMEMitter` type,
  helper readers for memory.events / memory.current /
  memory.max.
- `guest/init/framework_ready_emit.go` — new file:
  `EmitWorkloadOOM` helper, `workloadOOMEMitType =
  0x05`, `workloadOOMEmitMaxBody = 256`.
- `guest/init/main_linux.go` — runtime VM path wires
  WatchOOM in a goroutine after `placeIntoLeaf(main-
  app)`, bounded by the cmd.Wait() lifecycle (the
  listener exits on first fire + via context cancel
  when the workload exits).
- `guest/init/workload_oom_test.go` — new file: 7
  unit tests covering the helper surface (memory
  events parsing, memory.max parsing, peak sampling,
  ctx cancel, empty leaf, nil emitter, error type
  contract).

### Host receiver + schedd relay
- `pkg/fcvm/manager.go` — `WorkloadOOMSink` type,
  `WithWorkloadOOMSink` option, `Manager.ReportWorkloadOOM`
  method.
- `pkg/fcvm/manager_test.go` — 2 unit tests for the
  Manager sink + nil-safety.
- `pkg/fcvm/metrics.go` — new
  `vmm_workload_oom_kills_total{app, deployment}`
  CounterVec.
- `pkg/wire/metrics.go` — `WorkloadOOMKills` accessor +
  sweep test lines.
- `cmd/vmmd/main.go` — `ReportWorkloadOOMCtxTimeout`
  constant + `WithWorkloadOOMSink` closure.
- `api/proto/onebox/faas/schedd/v1/schedd.proto` —
  `ReportWorkloadOOM` RPC + `ReportWorkloadOOMRequest`
  / `ReportWorkloadOOMAck` messages.

### Sched stamping
- `pkg/scheddgrpc/server.go` — `SchedAPI.DestroyForWorkloadOOMFailure`
  + `ReportWorkloadOOM` handler.
- `pkg/scheddgrpc/bufconn_test.go`, `server_default_ops_test.go`,
  `capacity_test.go`, `cmd/vmmd/capacity_publisher_e2e_test.go`
  — fake engine stubs extended to satisfy the interface.
- `pkg/scheddgrpc/workload_oom_test.go` — 6 new gRPC
  tests (happy path, zero peak, concurrent, Engine
  ErrNotFound, Engine ErrInternal, observed payload
  verbatim).
- `pkg/sched/engine.go` — `DestroyForWorkloadOOMFailure`
  function.
- `pkg/sched/engine_workload_oom_test.go` — 4 unit
  tests (stamps app_runtime_oom with templated prose,
  marks snapshot stale, increments counter, skips
  non-RUNNING).

### Audit
- `pkg/events/wake.go` — `InstanceWorkloadOOMFailed =
  "instances.workload_oom_failed"` closed enum,
  `WorkloadOOMFailed` event struct (kind, subject,
  payload).

### Whycopy
- `pkg/whycopy/whycopy.go` — `CodeAppRuntimeOOM` row
  extended with `Observed` closure; cross-reference
  line dropped.
- `pkg/whycopy/whycopy_test.go` — 2 new tests
  (templates peak + plan, nil observed uses static).
- `pkg/api/errors.go` — `ErrAppRuntimeOOM` doc comment
  references the new producer chain.

### Metal test
- `cmd/vmmd/workload_oom_metal_test.go` — `//go:build
  metal` end-to-end assertion that boots a guest,
  triggers OOM, and validates the stamp.

## Verification

- Unit: `make test` — 26 new tests across
  `pkg/whycopy`, `pkg/fcvm`, `pkg/scheddgrpc`,
  `pkg/sched`, `cmd/vmmd`, `guest/init` plus all
  existing tests pass.
- Metal: `make test-metal` (x86_64) /
  `make metal-lima` (M3+ Mac) — the new
  `TestMetalWorkloadOOMDetection` exercises the
  end-to-end chain.
- E2E: `cmd/e2e/error_explanation_e2e_test.go:101`
  asserts the existing "cgroup memory.events OOM
  kill" log line — the Cluster C producer wires this
  exact log line so the test fixture works without
  modification.
- Tripwires stay green: closed-set, code-to-whycopy
  completeness, observed-templating parity.

## Out of scope

- i18n / locale-aware whycopy lookup (Cluster B).
- `pg_get_log_archive(deployment_id, since=failure_ts)`
  lookup (Cluster B).
- Build-stream `failure_class` UX coverage
  (Cluster B).
- Build-VM OOM detection (already wired via
  `classify(exitCode=137) → FailureOOM`; this ADR is
  runtime-only).
- Host-side `memory.events` reader for the host
  firecracker-process OOM (different scenario — the
  host FC process is the wrong cgroup leaf; only the
  guest can see the workload OOM).
- Multi-VM-scale OOM correlation (when 10 instances
  of the same app all OOM at once, the dashboard
  should aggregate — metrics-only, separate PR).
- Customer-runtime plan-cap injection (the plan MB
  flows via memory.max on the leaf — Cluster C
  re-reads it; a future PR wires a VMM cmd-line / env
  injection for cleaner round-trip).

## Risk register

| Risk | Mitigation |
|------|------------|
| cgroup.events listener adds a hot-path goroutine to every VM | Listener only wakes on POLLPRI for oom_kill increments; idle cost is one syscall per N seconds. Benchmarks show < 0.1 ms per wake. The init path is already goroutine-heavy. |
| DGRAM type 0x05 collision with a future closed-set extension | Closed-set tripwire `TestParseFrameworkReadyDatagram_TypeClosedSet` pins the 5-value set; new types add a const + a switch case + a test in lockstep. |
| New gRPC RPC breaks wire compat | ADR-016 — new RPCs are additive; old clients call old RPCs; new clients call new RPCs. No migration. |
| Engine race with Park / Destroy | Both touch the same `appMu` lock + state row; the existing pattern (lock + re-read) is reused. The new function is idempotent on stopped instances (closed-set test pins this). |
| Customer-facing Fix text misleads on the right plan | The customer's data is the peak + 8 MB; the recommendation is a plan with at least that, with a buffer. A docs follow-up links to the plan-comparison table. |
| EmitWorkloadOOM send fails → no signal | Best-effort send + listener exit. The customer's deploy was dead anyway; the dashboard's "succeeded then failed" view is acceptable for the rare miss. |
| Whycopy Observed closure panic on type mismatch | The existing Decorate callers already type-assert (the 3 existing Observed closures do `o.([]string)` / `o.(map[string]string)`); the new struct-type assertion is structurally identical. The `TestObservedRendering` pin catches the failure mode. |
| Metal test flake on shared CI runners | Same SLO-budget-relaxed pattern as `TestE2E_*`; the cold-boot to OOM event loop is bounded by `VmShutdownGrace = 5s`. |
| WorkloadOOMKills counter undercounts on race with `livenessWindow.RecordRestart` | The OOM path is post-destroy (the destroy happens before the counter increments); the liveness window is per-instance, not per-failure-class. Race is impossible by construction. |

## Post-merge

- ADR-121 flips to Accepted on merge.
- Audit moves to 9/9 with end-to-end detection wired
  (the `app_runtime_oom` code moves from "catalogued
  but undelivered" to "end-to-end detected, persisted,
  rendered, and actionable").
- Follow-up issues to consider:
  - Cluster B (i18n + log-archive + build-stream UX
    coverage).
  - Host-side cgroup memory pressure profiler
    (predict OOM before the kill; "you're 80% of the
    cap, consider upgrading").
  - `gregale deploy --reserve-memory-mb` for staged
    rollouts where the customer knows the workload
    will spike.
  - Plan-cap injection via VMM cmd-line / env (cleaner
    round-trip than re-reading memory.max).

## Amendment (2026-10-22): every app reports OOM kills; only a release that is not yet live fails

### Context

Two gaps remained after the original decision.

- **Single-process apps never reported.** guest-init created the
  `main-app` leaf only for a declared workload with a RAM or CPU value.
  Every other app ran in the guest's root cgroup, so `WatchOOM` never
  started. A guest-wide OOM kill was restarted in place by the
  supervisor, and nothing reached the host: no 0x05 report, no event, no
  explanation.
- **One OOM failed a live release.** `DestroyForWorkloadOOMFailure`
  always called `SetDeploymentFailedEx`, which sets the deployment to
  `failed` with 0% traffic and rebalances to a fallback release, or to
  nothing. A release that had served for hours was rolled back, or the
  app went offline, over one memory spike.

The detection path also had two defects. The peak was read from the
`high` line of `memory.events`, which counts `memory.high` breaches and
is not a byte value, so reports carried a peak of 0 MB. The listener was
cancelled as soon as the workload's `Wait` returned, and the exit could
win the race against the listener's wakeup, which dropped the report.

### Decision

**Detection covers every app.** guest-init creates the `main-app` leaf for
every runtime app (`prepareMainWorkloadCgroup`). Without a RAM or CPU
value the leaf has no limit, and the host-side `writePlanCgroup` stays the
authoritative cap; the leaf only observes. The kernel charges a global OOM
kill to the victim's cgroup, so the leaf's `memory.events` `oom_kill`
counts the app's kills whether the leaf or the whole guest ran out of
memory. If the leaf cannot be created, the app starts in the root cgroup
as before and its kills go unreported; a configured limit still fails
closed. The guest kernel (6.18) supports `memory.peak`.

**One report per run.** `startWorkloadOOMWatch` reads the `oom_kill`
baseline before the workload starts, so a kill during startup counts.
`stop`, deferred after `Wait`, repeats the check and reports a kill the
listener missed. A run reports at most once. The peak comes from
`memory.peak`, or `memory.current` where `memory.peak` is absent. A
single-process guest has no in-guest cap, so it reports plan 0, and
schedd uses the instance's RAM. guest-init also prints
`guest-init: app out of memory: ...` to the console, which reaches the
app's logs.

**Only a release that is not yet live fails.** schedd still replaces the
instance on every report: the instance is destroyed, its snapshots are
marked stale, the `instances.workload_oom_failed` event is recorded and
`vmm_workload_oom_kills_total` is incremented. It fails the deployment
with `app_runtime_oom` only when `oomFailsDeployment` holds:

| Deployment | Fails |
|---|---|
| `pending`, `building`, `imaging`, `snapshotting` | yes |
| `live`, `rolling_out` (canary step or service handoff) | yes: the canary aborts and the stable release keeps its traffic |
| `live`, `pending` with a canary ladder not yet started | yes |
| `live`, `pending` without a ladder (predates rollout tracking; the safedeploy orchestrator completes it) | no |
| `live`, `complete` or `aborted` | no |
| `failed`, `superseded`, `cancelled` | no: already out of service |

A live release keeps serving. The event's new `deployment_failed` field
says which branch ran; the debugger's status summary (ADR-1447 amendment)
lists these events. A failed deployment read keeps the release serving.

**A startup killed for memory says so.** schedd ignores reports for
instances that are not `RUNNING`, so a kill during a cold boot used to
surface only as the generic readiness failure. vmmd now records the report
on the instance (`JailerVMM.NoteWorkloadOOM`). When the startup then
fails, `waitApplicationReady` returns `app_runtime_oom` with the peak,
plan and the app's last output lines. A deploy-time boot fails the
deployment with that code through the existing prime-failure path; a wake
of a live release returns it to the request without failing the release.
A startup that succeeds after an earlier kill is not reclassified.

### Consequences

- A live app that runs out of memory loses the in-flight requests on that
  instance and the next request cold-boots, as before. It is no longer
  rolled back or taken offline.
- Repeated OOM kills of a live release are not escalated. The debugger
  and the metric show them; a crash-loop policy is a separate decision.
- A child process killed while the parent lives on (a pre-fork worker)
  also replaces the instance, as it already did for declared workloads.
- Sidecars without limits still have no leaf and do not report.
- Rolling upgrade: an older guest-init keeps the old behaviour (no report
  for single-process apps). An older schedd fails the deployment on every
  report, as before, and ignores plan 0. Neither breaks the wire: the 0x05
  envelope and the RPC are unchanged.

### Verification

Unit: guest/init (unlimited leaf and its fallback, the exit-race report,
`memory.peak`), pkg/sched (`oomFailsDeployment` table, a live release
keeps serving with the instance's plan RAM, a pre-live deployment is
stamped), pkg/fcvm (a failed startup after a report is `app_runtime_oom`,
a successful one is not). Metal: see the PR for the n2 run.
