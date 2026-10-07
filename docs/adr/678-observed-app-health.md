# ADR-678: Observed application health

Status: accepted · 2026-10-05

## Context

An active app row expresses intent, not whether requests can be served. Replica
state, reversible readiness transitions, deployment history and request metrics
already exist, but customers must interpret them separately. The console's
“Running” label can overstate what an active row establishes.

## Decision

Add a read-only `GET /v1/apps/{slug}/health` projection and expose it through the
CLI and customer app overview. It does not persist a new health state, schedule
work, wake a VM, make probes, or change routing and readiness ownership.

Use `healthy`, `degraded`, `unhealthy`, and `unknown`, with separate lifecycle
phases. Every verdict includes safe evidence checks and relevant existing
inspection destinations. Known failures outrank warnings; warnings outrank
unknown checks; unknown evidence prevents an otherwise healthy verdict.

The first slice assesses default-scope request and HTTP service workloads.
Worker/job execution is explicitly unassessed. Request telemetry is app-wide
across all scopes; the response states this limitation. Dark and non-default
releases, mirrors and command/job tasks do not contribute serving capacity.

Continuous readiness uses each required primary-app and primary-ingress
companion source independently. Missing sources are unknown. Their durable
transition timestamps are not probe heartbeats and are not expired. Running
instances also require node liveness evidence, using the existing heartbeat
staleness constant. A draining node may continue serving; a recovering node is
unconfirmed. No node addresses or raw probe/backend/deployment error text are
returned.

Scale-to-zero with cold-boot artifacts and no warm target is expected idle,
not a replica failure. A zero service target is intentionally stopped. A failed
latest deployment warns separately when an older release still serves. A
traffic-bearing service release with no ready replica warns even if the total
replica target is met.

Use three bounded-window Prometheus queries for total requests, 5xx requests
and actual sample freshness. All responses are in the denominator; only 5xx
are failures. Any observed 5xx warns without inventing an SLO or a severe-error
threshold. Latency and fleet wake metrics do not control this verdict. Missing,
stale or failed reads are unknown, including the normal absence of unexercised
traffic samples. Structural reads are available on every plan; request telemetry
retains the existing Hobby+ entitlement.

Limits are centralized in `pkg/api/limits.go`: 256 active instance rows with an
extra sentinel, 50 recent deployment rows, a 5-minute metrics window, 2-minute
sample/assessment validity, and a 10-second collection timeout. An incomplete
instance scan or missing default-scope history is explicit. Current live
releases use the existing live-deployment read, not historical instance scans.
No intermediary caching; the console refreshes every 30 seconds and expires a
cached assessment after server-provided validity. Error/unreachable states
supersede cached success.

## Consequences

This is a diagnostic assessment of observed evidence, not an endpoint uptime
or cold-wake guarantee. An unknown check can coexist with a known failure.
Unsupported scopes/classes and per-app aggregate metrics limit the first slice.
No migration or VM lifecycle acceptance is needed because the change only reads
existing evidence. Scenario, authorization, client contract and UI freshness
checks cover this slice; public probes, health history, alert transitions and
workload-specific assessments remain subsequent work.

The follow-up in [ADR-679](593-scoped-health-diagnostics.md) replaces app-wide
request evidence and the any-5xx policy with scoped diagnostics and explicit
severity defaults.
