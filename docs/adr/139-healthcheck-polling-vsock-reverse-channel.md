# ADR-139 · HEALTHCHECK polling protocol (vsock 1029 reverse-channel)

- **Status:** proposed
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [Epic #1186 — make Gregale a first-class OCI container platform](#1186), sub-task D.4 (HEALTHCHECK polling). M-2 of the four-Mega-PR plan.

## Context

M-1 surfaced the OCI `HEALTHCHECK` field on `AppManifest.Healthcheck`
(`pkg/api/appmanifest.go:62-72`) with shape
`{Test []string, IntervalS, TimeoutS, Retries, StartPeriodS int}` —
mirroring the OCI image-config `Healthcheck` schema. **No runtime
honours it today.** The only host-side probe is `ADR-057`
(`pkg/fcvm/vmm.go:2549-2619` `waitReady`), which does an HTTP `GET /`
(or `:8080` TCP-accept) on a customer-configured `HealthcheckPath` —
orthogonal to OCI `HEALTHCHECK`.

A real OCI image's HEALTHCHECK is rarely curl-shaped. Examples from
real registries:

- `HEALTHCHECK CMD curl -f http://localhost:8080/health || exit 1`
- `HEALTHCHECK CMD-SHELL redis-cli ping | grep PONG`
- `HEALTHCHECK CMD python3 /opt/check.py --strict`
- `HEALTHCHECK NONE` (no probe at all — common to base images)

Three plausible execution models:

1. **Exec inside guest via vsock reverse-channel** — guest-init owns a
   poll goroutine that runs the command, reports pass/fail to host via
   vsock. Closest to Docker semantics.
2. **Host-side exec via vsock forward-channel** — host dials guest's
   /bin/sh, runs the command, captures exit. Adds latency and a per-
   probe channel.
3. **HTTP probe translation** — narrow; only works for curl-shaped
   HEALTHCHECK; breaks Docker semantics for non-HTTP checks.

The M-1 path is **orthogonal to ADR-057** (HTTP `HealthcheckPath` is
the deployment-level HTTP probe; OCI `Healthcheck` is the image-level
exec probe). ADR-057 is preserved verbatim.

## Decision

### 1. New vsock **1029 DGRAM** channel (guest → host)

```
+---------------------------+
| HealthcheckReport         |
+---------------------------+
| seq    uint32             |  monotonic per-guest; wraps at 2^32
| status {pass,fail,start}  |  enum
| output []byte (≤ 4096)    |  ring-buffered stdout+stderr
| ts_unix_ms int64          |  guest wall clock at poll completion
+---------------------------+
```

DGRAM (datagram) is the right transport — connectionless, fire-and-
forget; the host treats the **absence** of messages for
`IntervalS × Retries` as `fail`. ReportCh queue size is **16
(drop-oldest)** so a slow host doesn't backpressure the poll loop.

### 2. Guest-init owns the poll goroutine

`guest/init/healthcheck_linux.go::runHealthcheckPoll(manifest *api.AppManifest,
reportCh chan<- HealthcheckReport)`:

- **Parses `manifest.Healthcheck.Test`** (the OCI argv[0] keyword):
  - `CMD` → exec argv[1:] directly via `exec.Command(argv[1], argv[2], ...)`
  - `CMD-SHELL` → exec via `/bin/sh -c argv[1]`
  - `NONE` → early return; no goroutine; no DGRAM traffic; existing
    `:8080` TCP-accept readiness probe continues to gate `waitReady`
  - **empty `Test` slice** → treated as `NONE`
- **Spawns `exec.Cmd` with `SysProcAttr.Credential{Uid: app UID,
  Gid: app GID}`** — runs as the manifest's User inside the same
  cgroup as the main workload (no shared host dirs; no root).
- **Timeout per attempt**: `cmd.Wait()` race with `time.NewTimer(TimeoutS)`;
  on deadline → `cmd.Process.Kill()` and report `fail{output=...}`.
- **Output capture**: ring buffer (existing `guest/init/ring_buffer.go`)
  with the last 4 KiB of stdout+stderr; surfaced in the
  `HealthcheckReport.Output` field for the operator.
- **Poll cadence**: `time.NewTicker(IntervalS)`. First poll fires at
  t=0 (during `StartPeriodS`); thereafter every `IntervalS`.
- **Restarts in `StartPeriodS`** are downgraded from `fail` to
  `starting` (does not count toward `Retries`). This matches Docker
  semantics exactly.

### 3. Host wiring (`pkg/fcvm/vmm.go::waitReady`)

When `appSpec.Healthcheck != nil` (and `Healthcheck.Test` is not
empty / not NONE), `waitReady`:

1. Opens vsock 1029 DGRAM dialer.
2. Waits up to `StartPeriodS` for the **first** `HealthcheckReport`.
3. First `pass` → return success; transition instance to RUNNING.
4. First `fail` → keep waiting until `StartPeriodS` expires; if no
   `pass` before deadline → return `startup_fail`.
5. After RUNNING: each `fail` increments the host's consecutive-fail
   counter; on `Retries` consecutive → return `liveness_fail` (existing
   ADR-079 liveness chain takes over).
6. Each `pass` resets the consecutive-fail counter to 0.

### 4. `HEALTHCHECK NONE` semantics

Empty `Healthcheck.Test` slice → no goroutine, no DGRAM, no impact on
the `:8080` TCP-accept readiness probe (existing ADR-057). This matches
Docker semantics exactly — `HEALTHCHECK NONE` declares "I have no
healthcheck" and the platform must NOT invent one.

### 5. No wire-format change

The existing vmmdpb / schedd proto surface is unchanged. Vsock channel
**1029 DGRAM** is convention, not schema. The host-side probe is
implemented entirely in `pkg/fcvm/vmm.go` and the `pkg/sched` adapter
layer; no proto regen is required for ADR-139 itself.

## Consequences

### Positive

- **OCI `HEALTHCHECK` semantics are honoured exactly.** Every Docker
  HEALTHCHECK shape (CMD / CMD-SHELL / NONE) maps 1:1.
- **Exec happens inside the guest as the manifest's User.** No host-
  side exec; no cross-namespace pollution; no jailer escape (uid is
  the manifest UID; cgroup is the instance's leaf).
- **Output capture is bounded (4 KiB ring buffer)** — no fd/memory
  growth under a HEALTHCHECK that prints megabytes per run.
- **No wire-format change.** M-2 ships with no proto regen for
  ADR-139 (commit 8 is purely guest-init + `pkg/fcvm`).
- **NONE semantics are clean** — no goroutine, no DGRAM traffic,
  falls back to the existing `:8080` TCP-accept readiness probe.

### Negative

- **Vsock channel 1029 is now load-bearing.** The cross-PR slot
  precheck convention already validates vsock channel numbers against
  a closed set (per ADR-022 / ADR-078 / ADR-079); 1029 is appended to
  that set on commit 8.
- **Guest-init grows ~300 LOC** for the poll loop. PID 1 already
  has the supervisor and the resume hook; the poll goroutine is the
  third concurrent goroutine. Backpressure on reportCh is bounded
  (drop-oldest); the poll goroutine itself never blocks on the host.
- **Process-group leakage risk.** Each HEALTHCHECK run spawns a child
  of guest-init; if the child outlives the probe timeout, the
  `cmd.Process.Kill()` fallback must reap it. Verified in
  `guest/init/healthcheck_test.go::TestHealthcheckPoll_TimeoutKillsChild`.

### Neutral

- ADR-057's HTTP-`HealthcheckPath` probe is **not removed**. The two
  probes coexist: OCI `Healthcheck` (image-level, exec) +
  `HealthcheckPath` (deployment-level, HTTP). Both can be set; OCI
  wins for readiness if both are present, and `HealthcheckPath` is
  the legacy fallback for customers who haven't migrated to OCI
  HEALTHCHECK yet.
- ADR-079's liveness probe (host-driven, vsock 1028) is preserved
  and now consumes the `fail` reports from vsock 1029 as its
  failure signal. Liveness cadence stays host-driven.

## Rejected alternatives

- **HTTP probe translation.** Rejected — narrow; many real HEALTHCHECK
  commands aren't curl-shaped. The exec-in-guest model matches Docker
  exactly.
- **Host-side exec via vsock forward-channel.** Rejected — adds
  latency (host dial + exec) and a per-probe channel. Guest-init
  already has the right cgroup, uid, and namespace.
- **Sidecar-based probe (`ping` sidecar container per deployment).**
  Rejected — adds a workload to every instance; ADR-069's
  SidecarCapMax=2 cap would be consumed by the probe alone for some
  deployments.
- **HEALTHCHECK output unbounded.** Rejected — operators need the last
  few KiB for debugging; the 4 KiB ring buffer is the right shape.
  Unbounded output would let a HEALTHCHECK flood the host vsock
  channel.
- **Connection-oriented (STREAM) vsock.** Rejected — DGRAM is the
  right fit: fire-and-forget; absence is the failure signal; no
  connection state to track.

## Cross-references

- **Forced by Mega-PR #2 (M-2) of issue #1186**:
  - `guest/init/healthcheck_linux.go` (commit 8, new)
  - `guest/init/listen_healthcheck_linux.go` (commit 8, new) — opens
    vsock 1029 DGRAM, sends reports
  - `guest/init/main_linux.go` (commit 8) — wires the goroutine
  - `pkg/fcvm/vmm.go::waitReady` (commit 8) — HEALTHCHECK-mode wait
  - `pkg/fcvm/vmm_healthcheck_test.go` (commit 8) — TestVMM_HealthcheckDial_ReceivesPass
  - `guest/init/healthcheck_test.go` (commit 8) — portable tests for
    CMD / CMD-SHELL / NONE shapes
  - `pkg/fcvm/vmm_healthcheck_metal_test.go` (commit 8) —
    TestMetalHealthcheckCmd_EndToEnd

- **Loading constraints (existing ADRs this PR must not violate)**:
  - ADR-022 (post-restore resume hook over AF_VSOCK): 1029 is the
    next channel after 1024 (resume). Existing channel assignments
    are unchanged.
  - ADR-078 (framework-ready signal vsock 1027): orthogonal. 1027
    is connection-oriented STREAM; 1029 is connectionless DGRAM.
  - ADR-079 (liveness probe vsock 1028 host-driven): preserved;
    now consumes vsock-1029 `fail` reports as the failure signal.
  - ADR-057 (runtime healthcheck probe HTTP `HealthcheckPath`):
    orthogonal; legacy HTTP probe retained for deployments without
    an OCI HEALTHCHECK.
  - ADR-019 (jailer uid 20000-29999): HEALTHCHECK exec runs as the
    manifest UID inside the same cgroup leaf. No host jid
    involvement.
  - ADR-069 (sidecar hard cap 2): unaffected. HEALTHCHECK does not
    consume sidecar slots.
  - ADR-011 (tenant security hardening §11): exec runs under the
    manifest User; output is bounded; no shared host dir.

- **Issue / PR relationships**:
  - **#1186** (parent epic) — M-2 sub-task D.4.
  - **#474** (guest-init supervisor split) — orthogonal; M-2 commit
    7 absorbs #474's work, commit 8 layers HEALTHCHECK on top.

- **Spec sections**:
  - §D.4 (HEALTHCHECK polling) — this ADR.
  - §6.3 (wake budget <350 ms) — unaffected. HEALTHCHECK runs in
    parallel with the `:8080` TCP-accept probe; the first to fire
    `pass` wins.
  - §11 (security hardening) — exec runs as the manifest User; no
    shared host dir; output is bounded; ring buffer is on the
    guest's tmpfs.

- **Tests pinning this ADR**:
  - `guest/init/healthcheck_test.go::TestHealthcheckPoll_CMD_PassFail`
  - `guest/init/healthcheck_test.go::TestHealthcheckPoll_CMDSHELL_ExecViaSh`
  - `guest/init/healthcheck_test.go::TestHealthcheckPoll_NONE_NoGoroutine`
  - `guest/init/healthcheck_test.go::TestHealthcheckPoll_TimeoutKillsChild`
  - `pkg/fcvm/vmm_healthcheck_test.go::TestVMM_HealthcheckDial_ReceivesPass`
  - `pkg/fcvm/vmm_healthcheck_metal_test.go::TestMetalHealthcheckCmd_EndToEnd`