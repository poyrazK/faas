# ADR-20261123170000: Keep startup-OOM wakes out of the platform wake SLO

- Status: Accepted
- Date: 2026-11-23
- Related: ADR-121 (workload OOM reports and its startup amendment),
  spec §12 (platform wake boundary), PR #7942

## Context

`gateway_platform_wake_latency_seconds` is the release gate and the public
status page's wake p95 (target < 350 ms). On 2026-11-23 it reached 3.95–4.45 s
from one app: `voting-demo-vote` ran 4 gunicorn sync workers in a 256 MB VM.
Every cold boot stalled under memory pressure until the guest kernel
OOM-killed a worker (e.g. listening at +0.9 s, OOM kill at +4.7 s, vmmd
`wake ok` 161 ms later, `cold_boot_ms 4858`). Canary route probes woke it every
minute, and the instance was destroyed after each `workload_oom`, so each
round repeated the full cold boot. The platform did nothing wrong; the series
still reported the app's memory exhaustion as platform latency.

vmmd already learns of such kills before readiness (ADR-121 amendment,
`NoteWorkloadOOM`), but used them only to explain failed startups.

## Decision

1. vmmd reports `WakeResponse.startup_oom` when the guest reported a workload
   OOM kill before a successful wake returned. schedd carries it on `Wake`,
   `AdmitInstance` and `EnsureWake` responses (including burst siblings).
2. The gateway attaches a request-scoped recorder to every admission it makes
   for a request (pinned, exact-deployment, ensure-capacity, burst and
   fan-out). If any wake that request admitted reported `startup_oom`, the
   request's wake is excluded from `gateway_platform_wake_latency_seconds` and
   its phase breakdown, and counted in
   `gateway_platform_wake_excluded_total{reason="startup_oom"}`. The app's own
   wake latency keeps it. The decision follows the wake the request waited
   on, not the instance the final pick routes it to.
3. The fields are additive; older daemons never set them, so behaviour is
   unchanged until vmmd, schedd and gateway all run this version.

## Consequences

- An app that exhausts its RAM no longer moves the platform SLO or status page.
- Platform-caused OOMs (e.g. a regression that grows guest overhead so small
  plans OOM on boot, as on 2026-11-16) are also excluded. They stay visible in
  `gateway_platform_wake_excluded_total` and in the existing
  `app_runtime_oom` / workload-OOM counters; a fleet-wide rise in the
  exclusion rate must be treated as a platform signal.
- Best effort: the guest's OOM report races the readiness probe. A report that
  lands after the wake returned leaves that wake counted as platform latency.
  A wake that failed over after an OOM on another node reports only the final
  attempt.
