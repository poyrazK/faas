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

## Amendment 1 (2026-11-24): deployment smoke wakes

### Context

On 2026-11-24 the status page showed App execution degraded (wake p95
382–386 ms) with no customer wake over target. Over the previous 24 h the
platform wake series held 504 customer-triggered restores at p95 157 ms
(2 over 350 ms) and 172 `deployment.smoke` restores at p95 530 ms (24 over
350 ms). A deployment smoke is admitted while the candidate's first-boot
instance is still capturing the snapshot the smoke then restores: on
deployment `4520429d`, gateway admission started at 14.899 s, the first-boot
park ran 14.976–15.133 s, and schedd admitted the restore at 15.183 s; of the
394 ms recorded, about 280 ms was the deploy pipeline waiting for its own
snapshot. With light traffic the 30-minute window holds barely more than the
20 samples the status page needs, so a burst of deployments (here an agent
evaluation deploying one app 9 times in 10 minutes) moved the public figure.

### Decision

A wake admitted by an authorized deployment smoke verification is excluded
from `gateway_platform_wake_latency_seconds` and its phase breakdown and
counted in `gateway_platform_wake_excluded_total{reason="deployment_smoke"}`
(pre-instantiated at 0). The app's own wake latency still records it. Route
probes, previews, revision pins and project releases are unchanged: they are
requests a customer or the platform's routing makes against a deployment that
already has its snapshot.

The exclusion covers every cold wake a smoke admits, including a smoke that
re-verifies a deployment whose snapshot already exists (rollback, promote).
The gateway cannot tell whether a smoke waited on a first-boot park, and a
smoke is never customer demand either way.

A smoke whose wake also reported a startup OOM is counted under
`reason="startup_oom"`, not `deployment_smoke`: a release that grows guest
overhead OOMs candidates first, and that must still move the startup-OOM rate
the main decision treats as a platform signal.

### Consequences

- Deploy bursts no longer move the platform SLO or the status page; the
  window holds customer wakes only, so quiet windows report no p95 (fewer
  than 20 samples) more often instead of a deploy-weighted one.
- A regression that slows only the smoke restore path is no longer visible in
  the SLO series. It stays visible in the deployment receipt's smoke timing,
  the `wake.*` events (`trigger = deployment.smoke`) and the excluded counter.
- `gateway_wake_latency_seconds` (§12) and the per-app series are unchanged.
- `faas_slo_platform_wakes_within_350ms:ratio_30d` changes population at
  rollout: for 30 days it mixes pre-rollout smoke samples with post-rollout
  customer-only samples, so `FaasMonthlyPlatformWakeGateMissed`'s ratio
  climbs as the smokes age out (runbook `FaasMonthlySLO.md`).
- `scripts/ops/obs_gate.py` requires at least 20 platform wakes in its
  window and names a shortfall, rather than failing on a non-finite p95.
