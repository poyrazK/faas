# ADR-138 · Container lifecycle contract (startup / readiness / liveness / termination)

- **Status:** proposed
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [Epic #1186 — make Gregale a first-class OCI container platform](#1186), sub-task D.2 (lifecycle contract). M-2 of the four-Mega-PR plan.

## Context

Today, every customer instance lives one of two ways:

1. `RUNNING → PARKED` (idle reaped, snapshot captured, no signal sent)
2. `RUNNING → STOPPED` (cold boot fail, snapshot broken, or admin stop —
   all via `JailerVMM.Kill` which is a hard `cmd.Process.Kill()` →
   SIGKILL; see `pkg/fcvm/vmm.go:1256-1321`)

There is **no SIGTERM-with-grace path**, **no PID 1 signal handling** in
guest-init (no `os/signal` import; see `guest/init/main_linux.go:61-259`),
**no `Supervisor.Run(ctx)` cancellation hook** (`guest/init/supervise.go:184-214`),
and **no `AppManifest.StopSignal` wiring** (M-1 surfaced it; runtime
ignores it).

OCI images declaring `STOPSIGNAL SIGUSR1` and `StopGracePeriod 30s` are
stopped today by a SIGKILL after an `Engine.StopInstance(...)` that has
no signalling step — which violates Docker's container-kill semantics,
breaks drain operations on long-running daemons, and orphans any
worker child of the main workload.

M-1 deferred the runtime wiring; M-2 lands it.

## Decision

### 1. Stop sequence: signal → grace → SIGKILL

`JailerVMM.SignalAndKill(ctx, lease, signal syscall.Signal, grace time.Duration) error`
sends `signal` to the guest's PID 1 (via vsock), polls `cmd.Wait()`
with a `time.NewTimer(grace)` race; on deadline → falls through to
`cmd.Process.Kill()` (SIGKILL).

Wired through a new vmmdpb RPC `StopInstance(StopInstanceRequest{Signal
int32, GracePeriodS int32}) returns (StopInstanceResponse)` (commit 5).
The schedd's `Engine.StopInstance(ctx, instanceID, opts StopOptions)`
dispatches per `Instance.ExecutionMode`:

| Mode | Path | Cache effect |
|---|---|---|
| `request` | `snapshotAndPark` (existing) | snapshot preserved |
| `service` | `snapshotAndPark` (existing) | snapshot preserved |
| `worker` | `SignalAndKill` then `timedDestroy` | no snapshot |
| `job` | `SignalAndKill` then `timedDestroy` | no snapshot |

For workers/jobs, the **host sends the StopSignal** through vsock to
PID 1; guest-init translates and forwards to the main workload
(commit 7). The host never signals the main workload directly.

### 2. PID 1 signal handling (commit 7, closes #474)

`guest-init/main_linux.go::installSignalHandlers(sup *Supervisor,
manifestStopSignal string)`:

- **SIGTERM** (default when manifest `StopSignal` is empty) →
  `sup.Stop(ctx)`.
- **SIGCHLD** → `wait4(-1, ...)` reap loop (PID 1 obligation).
- **`manifestStopSignal`** (e.g. `SIGUSR1`, `SIGHUP`) → forwarded to
  the supervisor's tracked child process group via `Setpgid: true`.
- **Any other signal** (not the manifest's StopSignal) → ignored,
  logged at debug.

`Supervisor.Run` gains a `ctx context.Context` arg; on `ctx.Done()`,
sends the StopSignal (default SIGTERM) to the tracked child's
process group, waits up to `stopGrace` (default `MaxAppManifestStopGracePeriod`
until commit 10 tightens), then `cmd.Process.Kill()`.

Main workloads are spawned with `SysProcAttr{Setpgid: true,
Pdeathsig: SIGTERM}` so an orphaned main workload dies cleanly if
guest-init exits unexpectedly.

### 3. Lifecycle failure taxonomy (events row)

A closed-set enum surfaced on `instances.lifecycle_failure_reason`
(nullable; populated only when state transitions to FAILED or STOPPED
with non-success):

| Value | Trigger |
|---|---|
| `startup_fail` | did not reach ready within `StartupDeadlineS` |
| `readiness_fail` | HEALTHCHECK failed within `StartPeriodS` |
| `liveness_fail` | vsock-1028 host probe failed N consecutive |
| `crash_loop` | Supervisor exceeded `MaxRestarts` |
| `oom` | cgroup `memory.max` killed the workload |
| `clean_exit` | exit code 0 — success for `job`, restart for `worker`/`service` per RestartPolicy |
| `error_exit` | exit code ≠0 — failure for `job`, restart for `worker`/`service`/`request`-on-failure |

Surfaced as a single nullable column + an event row (`event_kind =
'lifecycle_failure'`) carrying the structured payload (exit code,
signal, restart count, last healthcheck output).

### 4. Per-plan caps (commit 10, on top of M-1)

| Field | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| `StartupDeadlineS` default | 15 | 30 | 60 | 120 |
| `StartupDeadlineS` max | 15 | 60 | 120 | 300 |
| `MaxRestarts` default | 0 | 3 | 5 | 10 |
| `MaxRestarts` max | 0 | 5 | 10 | 20 |
| `StopGracePeriodS` max | 15 | 30 | 60 | 120 |
| `WorkerReplicasMax` | 0 | 1 | 3 | 10 |
| `ServiceReplicasMax` | 0 | 1 | 5 | 20 |
| `JobMaxRuntimeS` | 60 | 600 | 3600 | 86400 |

The current M-1 gross `MaxAppManifestStopGracePeriod = 5*time.Minute`
is replaced. Hobby customers needing >30 s stop grace must set a
per-app override (≤30 s cap); for >120 s the Scale plan is the path.

## Consequences

### Positive

- **OCI `STOPSIGNAL` and `StopGracePeriod` are honoured** for the
  first time. A `docker stop`-shaped shutdown sequence (signal →
  grace → SIGKILL) is the new default for all modes.
- **No more orphaned children.** PID 1 reaps SIGCHLD; the main
  workload's `Pdeathsig: SIGTERM` ensures orphan reaping on the
  child side too.
- **Lifecycle failure taxonomy is closed-set and machine-readable.**
  Operators filter on `lifecycle_failure_reason`; customers see
  `deployment_error` in apid responses; the meter sampler can bill
  differently for `oom` (subtract from MB-second only the
  pre-OOM-signal interval).
- **Issue #474 closed** as a side-effect of commit 7. The
  guest-init supervisor split becomes the production default.

### Negative

- **PID 1 signal handling is high-stakes.** A regression in commit 7
  could break every customer wake. `/code-review` agent runs on
  commit 7; metal-lima gates the commit; per ADR-136 §Forced
  follow-ups, commit 7 is the smallest unit that can be reverted
  without unwinding the rest of M-2.
- **Per-plan tightening (commit 10)** is **tighter** than the M-1
  gross 5 min cap for Hobby customers. Documented in PR description
  + financial-model addendum (`docs/financial/m2_execution_mode_addendum.md`).
- **Worker/job mode changes the billable shape.** A worker idle for
  1 h at 256 MB Hobby = 0.264 GB-h (same formula, no skip). Customers
  who expected "free idle" must explicitly use `ExecutionMode='request'`.

### Neutral

- Mirror-mode skip (ADR-125) is preserved.
- ADR-057's HTTP-`HealthcheckPath` probe is orthogonal; it still gates
  `waitReady` for the legacy deployment field. New ADR-139 wires the
  OCI `HEALTHCHECK` via vsock reverse-channel (separate ADR).
- ADR-050's `WorkloadClass` is preserved verbatim.

## Rejected alternatives

- **Two-phase SIGTERM-then-SIGINT escalation.** Rejected — adds
  complexity without measured benefit; one signal is what Docker does,
  and the `StopSignal` field is the override.
- **Per-VM independent stop timer** (each instance has its own grace
  countdown from wake time). Rejected — `StopGracePeriod` is per-app
  and applied at stop time, not at wake time. The per-app shape
  matches OCI's `StopGracePeriod` semantics.
- **Clean exit handling for `request` mode** (today's behaviour:
  request-mode apps don't exit, they receive traffic). Rejected —
  `request` mode apps that exit cleanly are misconfigured; today's
  behaviour (idle reap + snapshot) is correct.
- **Sampling-based `lifecycle_failure_reason` inference.** Rejected —
  the enum is structured at the source; sampling would lose the
  `error_exit` code and the last-healthcheck output that operators
  need to debug.
- **Per-mode `StopGracePeriod` defaults (not just caps).** Rejected
  for M-2 — only the cap tier lands; defaults land in M-3 with the
  per-app override pattern.

## Cross-references

- **Forced by Mega-PR #2 (M-2) of issue #1186**:
  - `pkg/vmmdpb/vmmd.proto` + regen (commit 5) — new
    `StopInstance(Signal, GracePeriodS)` RPC
  - `pkg/fcvm/vmm.go::SignalAndKill` (commit 5)
  - `pkg/vmmdgrpc/server/` (commit 5) — RPC handler
  - `pkg/sched/vmmclient.go` (commit 5) — VMM interface widens
  - `pkg/sched/engine.go::Engine.StopInstance` (commit 6)
  - `guest/init/main_linux.go::installSignalHandlers` (commit 7)
  - `guest/init/supervise.go::Supervisor.Run(ctx)` (commit 7)
  - `guest/init/reaper_linux.go::reapLoop` (commit 7)
  - `guest/init/workload_linux.go` (commit 7) — Setpgid + Pdeathsig
  - `pkg/api/limits.go` (commit 10) — per-plan tier table
  - `pkg/api/appmanifest.go::Validate` (commit 10) — per-plan cap
    consult

- **Loading constraints (existing ADRs this PR must not violate)**:
  - ADR-005 (cold boot must always work): unaffected. The signal
    sequence is for `Engine.StopInstance` (admin/explicit stop); the
    idle-reap path still uses `snapshotAndPark` for `request`/`service`
    modes and never sends a signal.
  - ADR-019 (jailer uid 20000-29999): unaffected. Each instance gets
    its own uid regardless of mode; signal forwarding happens through
    PID 1 (the guest's init), not through the host jailer.
  - ADR-022 (post-restore resume hook over AF_VSOCK): orthogonal.
    Resume hook continues to dial vsock 1024 on restore; the new
    signal sequence is independent.
  - ADR-078 (framework-ready signal): orthogonal. Framework-ready
    fires at vsock 1027; the new HEALTHCHECK DGRAM channel is 1029
    (ADR-139).
  - ADR-079 (liveness probe — vsock 1028 host-driven): orthogonal.
    Liveness is host-driven; new lifecycle failure taxonomy is event-
    driven (guest-init emits `crash_loop`/`oom`/`error_exit` events).
  - ADR-057 (runtime healthcheck probe — HTTP-`HealthcheckPath`):
    orthogonal. ADR-139 wires the OCI `HEALTHCHECK` via vsock 1029.

- **Issue / PR relationships**:
  - **#1186** (parent epic) — M-2 sub-task D.2.
  - **#474** (guest-init supervisor split) — closed by M-2 commit 7.
  - **PR #1185** (durable async jobs, draft) — orthogonal; their
    async-job lifecycle has a different shape (queue-driven, not
    signal-driven).
  - **PR #1195** (jobs/mega1, OPEN) — orthogonal.

- **Spec sections**:
  - §D.2 (lifecycle contract) — this ADR.
  - §6.1 (state machine) — adds the `lifecycle_failure_reason` column
    + event row; no transition changes.
  - §6.3 (wake budget <350 ms) — unaffected. Signal handling is on
    the stop path; wake path is unchanged.
  - §4.7 (meterd — billing math) — formula unchanged; lifecycle
    failure events inform downstream audit rows.
  - §11 (security hardening) — signal forwarding respects the
    existing cgroup scope and jailer chroot.

- **Tests pinning this ADR**:
  - `pkg/fcvm/vmm_signal_kill_test.go` (commit 5) — SignalAndKill
    table-driven over (signal, grace, child-exit-within-grace)
  - `pkg/fcvm/vmm_signal_kill_metal_test.go` (commit 5) —
    TestMetalSignalAndKill
  - `guest/init/supervise_test.go` (commit 7) — TestSupervisor_StopsOnContextCancel
  - `guest/init/supervise_test.go` (commit 7) — TestSupervisor_GracefulThenKill
  - `guest/init/reaper_linux_test.go` (commit 7) — spawned grandchild
    reaped, no zombie
  - `guest/init/supervise_metal_test.go` (commit 7) —
    TestMetalGuestInitSignalForward
  - `pkg/api/limits_test.go` (commit 10) — per-plan cap table
  - `pkg/fcvm/vmm_lifecycle_metal_test.go` (commit 11) — table-driven
    lifecycle failure taxonomy