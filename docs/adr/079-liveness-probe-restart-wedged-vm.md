# ADR-079 · Liveness probe — restart a wedged VM

- **Status:** accepted
- **Date:** 2026-08-06
- **Issue:** #554 (Liveness probe — restart a wedged VM, Cloud-Run parity)
- **Supersedes:** none

## Decision

Per-deployment liveness probe. `cmd/vmmd` polls the guest via a
new vsock port **1028 STREAM** (`host→guest`, mirroring
ADR-022's resume-hook direction at port 1024); on `N`
consecutive non-2xx (or timeout / conn-refused) responses the
host declares the VM wedged and calls `Manager.ReportLivenessFailed`,
which schedd's `Engine.DestroyForLivenessFailure` consumes. The
destroy eagerly marks the deployment's latest snapshot stale so
the next Wake cold-boots from rootfs (per ADR-005 — never
restore a wedged snapshot). After **3 restarts in 300 s** the
parent app is parked (`apps.status='evicted_cold'`) and the
deployment is marked exhausted via the audit kind
`instances.parked_liveness_exhausted`.

| Plan | LivenessAllowed | period_s | consecutive | cooldown_s | max_restarts_in_window | window_s |
|------|-----------------|----------|-------------|------------|------------------------|----------|
| Free | false           | —        | —           | —          | —                      | —        |
| Hobby| true            | 5        | 3           | 60         | 3                      | 300      |
| Pro  | true            | 5        | 3           | 60         | 3                      | 300      |
| Scale| true            | 5        | 3           | 60         | 3                      | 300      |

gRPC liveness (Pro+) is deferred — v1 is HTTP only, mirroring
the existing readiness path at `healthcheck_path`.

## Why

Wedged guests (busy-loop ignoring SIGTERM, deadlocked runner,
leaked FD) sit resident billing RAM-hours while serving 5xx.
The §13 idle reaper at 60/60/300/600 s by plan is too slow for
a customer-facing outage. Cloud Run's primitive here is a
health-check-driven replace; we're matching that surface.

## Channels & wire shape

- **Vsock port:** `1028 STREAM` (host→guest). The guest-init
  listener (`guest/init/liveness_linux.go`) binds; the vmmd
  poll goroutine (`cmd/vmmd/liveness_recv.go`) dials the
  per-VM CID on every period.
- **Frame:** `[4B BE msg-type=10 (probe)] [4B BE body-len]
  [N-byte JSON {path, timeout_ms}]`. Response is the same
  envelope with msg-type=11 (ack), body
  `{status:int, err:string}` where err is the closed-set
  classifier ("timeout" / "conn_refused" / "runner_not_ready").
- **Existing channels preserved:**
  - 1024 STREAM — resume hook (ADR-022, host→guest)
  - 1025 DGRAM — stateless-advisory
  - 1026 STREAM — characterization (guest→host)
  - 1027 DGRAM — framework_ready (guest→host)
  - **1028 STREAM** — liveness probe (host→guest) ← new

## Idle reset on restart (user-confirmed)

`Engine.DestroyForLivenessFailure` calls `TouchInstancesLastSeen`
on the destroyed instance so the replacement cold-boot
instance's idle budget restarts fresh. Without this, the
reaper grace would still be counting against the wedged VM
that no longer exists.

## Metric + audit surface

- **Counter:** `<daemon>_liveness_restarts_total{app,deployment}`
  (`pkg/wire/metrics.go::OpsMetrics.LivenessRestarts`).
- **Histogram:** `vmmd_guest_liveness_probe_seconds{outcome}`
  with closed-set label {ok, non_200, timeout, conn_refused, conn_err}.
- **Gauge:** `vmmd_guest_liveness_consecutive_failures{instance}`
  per-instance consecutive-failure counter.
- **Audit kinds:**
  - `instances.liveness_failed` — emitted by
    `Engine.DestroyForLivenessFailure` per destroy
  - `instances.liveness_restarted` — convenience mirror
  - `instances.parked_liveness_exhausted` — emitted by
    `Engine.ParkDeployment` on the 3rd restart in the window

## Sliding window — in-memory

The per-deployment restart counter lives in
`pkg/sched/liveness_window.go` (in-memory). Schedd restart
mid-window loses the counter; the next Wake on an
already-parked deployment is a manual
`gregale deployment unpause`. If observed in prod, claim slot
150 and persist `deployments.liveness_restart_count`. The
slot 149 fence (`migrations/00149_reserve_slot.sql`) reserved
slot 150 for this kind of follow-up; we instead used it for
the `deployments.override_liveness_probe jsonb` column
(`migrations/00150_deployment_liveness_probe.sql`) — the
column the vmmd poll goroutine actually reads at every
BringUp.

## Spec sections updated

- §6.1 state machine — new row "RUNNING → STOPPED
  (reason='liveness_failed') — liveness probe N consecutive
  failures; next wake cold-boots because snapshot is
  stale-marked before destroy (issue #554)."
- §6.4 failure-mode catalogue — new row `| VM wedged,
  liveness probe fails N consecutive | RUNNING → STOPPED | 3
  | 60 s | 3_in_5min |`.
- §13 hard limits — new rows for `LivenessPeriodSeconds`
  (5 / 1 / 60), `LivenessConsecutiveFailures` (3 / 1 / 10),
  `LivenessCooldownSeconds` (60 / 10 / 600),
  `LivenessMaxRestarts` (3 / 1 / 10),
  `LivenessWindowSeconds` (300 / 60 / 3600).
- §15 limits mirror — list the new `DefaultLiveness*`
  constants.

## Rejected alternatives

- **Restore snapshot after failure.** ADR-005 forbids; the
  snapshot of a wedged VM is a wedged VM.
- **gRPC health on existing characterization port 1026.**
  Wrong direction (1026 is guest→host); mixing health-check
  traffic with characterization would couple two unrelated
  surfaces.
- **SIGTERM before destroy.** Already in the snapshot-stale
  flip; the metal test (`make test-metal`) proves the
  busy-loop rootfs is SIGTERM-immune → destroy.
- **Per-account vs per-deployment window.** Per-deployment
  matches §6.4 granularity (snapshot + park paths are
  per-deployment).
- **Persist `deployments.liveness_restart_count`.** Deferred
  (slot 150 reserved but not migrated as that column).

## Implementation pointers

- Migration slot fence: `migrations/00149_reserve_slot.sql`
- Per-deployment column: `migrations/00150_deployment_liveness_probe.sql`
- Limits + accessors: `pkg/api/limits.go` (5 fields + 5
  accessors + 7 §13 mirror constants)
- DTOs: `pkg/api/dto.go` (`DeploymentLivenessProbe` + 2
  override/references)
- Guest listener: `guest/init/liveness_linux.go`
- Host poll goroutine: `cmd/vmmd/liveness_recv.go`
- Metrics: `pkg/fcvm/metrics.go` (`LivenessMetrics`) +
  `pkg/wire/metrics.go` (`OpsMetrics.LivenessRestarts`)
- Engine: `pkg/sched/engine.go` (`DestroyForLivenessFailure`,
  `ParkDeployment`)
- Window: `pkg/sched/liveness_window.go`
- Loop wiring: `pkg/sched/loop.go` (`WithLivenessWindow`)
- Audit events: `pkg/events/wake.go` (`LivenessFailed`,
  `LivenessRestarted`, `ParkedLivenessExhausted`)
- Handler: `cmd/apid/handlers_sidecars.go`
  (`applyOverridesToDeployment`), `cmd/apid/handlers_ext.go`
  (`fillDeploymentResponseOverrides`)
- Tests: `pkg/sched/liveness_window_test.go`,
  `pkg/sched/engine_liveness_test.go`,
  `cmd/vmmd/liveness_recv_test.go`
