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

## Amendment 1 (2026-11-12): plan-default liveness checks that the server answers

**Status:** accepted (owner, 2026-11-12).

**Context.** The decision above says liveness mirrors `healthcheck_path`,
but vmmd sent every app without a liveness override to the plan-default
`/healthz` and counted any non-2xx as a failure. In production on
2026-11-12 all five `retail-*` apps (Spring/Express images readying on
`/actuator/health` or `/health`) answered `/healthz` with 404, so each
instance was destroyed about 10 s after it booted. The destroy marks the
snapshot stale, so every request paid a 12–13 s JVM cold boot and three
restarts in 300 s parked the apps. gRPC apps fared worse: an HTTP/1.1 GET
cannot reach a gRPC server, so `pulsewatch-grpc` failed with
`liveness_conn_err` after every boot. H10-23 had already exempted 404 for
TCP-readiness images only.

Probing the declared health path instead would kill a healthy process
whenever a dependency fails: Spring's `/actuator/health` returns 503 while
its database is down, so a 15 s database outage would restart the app and
three restarts would park it (the data API already uses a separate
`/livez` for this reason, #6837).

**Decision.** With no liveness path or gRPC action declared, the
plan-default probe asks whether the server still answers, never whether
its dependencies are healthy:

| Startup contract | Plan-default liveness |
|---|---|
| HTTP readiness on `/healthz` | `GET /healthz`, 2xx only (unchanged) |
| HTTP readiness on another path | `GET /healthz`; any answer below 500 is alive |
| TCP readiness (no health path), HTTP image | `GET /healthz`; any answer below 500 is alive (widens H10-23 from 404) |
| TCP readiness on a declared raw TCP listener (ADR-20261027090000) | none; a crash still reports through process exit |
| gRPC readiness, Pro/Scale | health.v1 `Check` on the service readiness used |
| gRPC readiness, Free/Hobby | none (gRPC liveness stays Pro+, `GRPCLivenessAllowed`); a crash still reports through process exit |

A timeout, a refused connection or a 5xx still counts as a failure, so a
wedged or crashed server is still replaced.

The guest's liveness GET follows a redirect only while it stays on the
probed origin (scheme and host:port), so trailing-slash and `/login`
redirects still reach their page. Any other redirect returns the 3xx
itself. Before, an app that sends `/healthz` to `https://…` (Spring
`requiresChannel`, Django `SECURE_SSL_REDIRECT`, Rails `force_ssl`, Phoenix
`force_ssl`) made the probe speak TLS to a plain-HTTP port, and an app that
redirects to an outside login page sent the probe out of the VM; both
failed as transport errors. On a path the app never promised the 3xx is
alive; on a promised `/healthz` it fails, as the startup probe would.

A raw TCP server answers an HTTP GET in its own protocol or waits silently
for a frame, so no HTTP probe can judge it. schedd marks TCP readiness on a
declared raw TCP listener (`AppSpec.routed_port_raw_tcp`) and vmmd runs no
default probe for it. 401/403 on a path the app
never promised no longer marks the deployment `app_healthz_unauthorized`;
on a promised `/healthz` it still does. An explicit liveness override is
unchanged and stays strict. Worker, job and image-HEALTHCHECK exemptions
are unchanged. The adoption record carries the readiness gRPC service
and the raw TCP mark (`omitempty`), so a vmmd restart keeps the same
probe. An older vmmd's strict decoder refuses to adopt only an instance
that carries one of them, and that instance is replaced.

Old and new components mix safely: an older vmmd ignores
`routed_port_raw_tcp`, and an older guest-init keeps following every
redirect, which is today's behavior.

**Implementation.** `pkg/fcvm/manager.go` (`planDefaultLivenessAction`,
`LivenessProbeConfig.UnpromisedPath`), `cmd/vmmd/liveness_recv.go`
(`classifyLivenessStatus`; a reported failure now logs path, port and the
last HTTP status), `guest/init/liveness_redirect.go`,
`pkg/sched/deployment_runtime.go` (`routedPortRawTCPForApp`),
`api/proto/onebox/faas/vmmd/v1/vmmd.proto` (`routed_port_raw_tcp = 45`),
`pkg/fcvm/resource_adoption.go`. Tests: `pkg/fcvm/worker_liveness_test.go`,
`cmd/vmmd/liveness_recv_test.go`, `guest/init/liveness_redirect_test.go`,
`pkg/sched/raw_tcp_readiness_test.go`.

## Amendment 2 (2026-11-13): a defaulted readiness path is TCP readiness

A source build whose health path is the platform's `/healthz` default
(neither the source nor `gregale.yaml` names a health route) marks its
profile `health_defaulted`. sched then gives it TCP-accept readiness, as a
direct OCI image has, and the hosting smoke checks route connectivity; the
plan-default liveness probe treats `/healthz` as unpromised (any answer
below 500 is alive). A plain Express or Flask app answered `/healthz` with
404 and every deploy ended `app_startup_timeout`. A declared path (deploy
override, manifest, `gregale.yaml`, or a route found in the source) keeps
strict HTTP readiness. Profiles written before this lack the mark and keep
HTTP readiness until redeployed.
