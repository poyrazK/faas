# Application health

Read the current assessment without waking your app:

```sh
gregale app my-api health
gregale --json app my-api health
```

The equivalent API is `GET /v1/apps/{slug}/health`, authorized by `apps:read` or
`admin`. App ownership is checked before collecting evidence. Structural
checks are available on every plan; request telemetry follows the existing
Hobby+ metrics entitlement.

| Status | Meaning |
| --- | --- |
| `healthy` | Available, current evidence shows no health issues. |
| `degraded` | A warning needs attention, such as a partial replica deficit, recent 5xx or a failed latest release while an older release serves. |
| `unhealthy` | Known evidence shows a serving failure, such as no ready required replicas or all observed serving replicas unready. |
| `unknown` | Missing, stale, unavailable or truncated evidence prevents confirmation. |

Each response includes `checks` with a stable `code`, status, explanation and
an optional inspection action. Lifecycle phases such as `idle`, `deploying`
and `stopped` are separate from confidence in health. The customer app overview
links checks to existing release, log, error, metrics and configuration views.

Readiness combines running replica state, the independent required primary-app
and primary-ingress companion probes, and current node heartbeat evidence.
Optional companion probe failures do not gate the main route. The warm-snapshot
framework-ready timestamp is not used as a serving health signal. A service's
zero replica target is intentional; a request workload with no warm target and
cold-boot artifacts can be normally idle. The read never tests a cold wake.

Request evidence covers the last **5 minutes**, including **all app scopes**.
Only 5xx responses count as failures. Any observed 5xx produces a warning;
4xx alone does not. No request traffic means success has not been exercised.
An actual telemetry timestamp is required and must be within **2 minutes**.
A successful fetch with missing/stale timestamps cannot confirm request health.

Assessment timestamps expire after **120 seconds**. Clients should refresh
rather than continue displaying a healthy cached result. The console refreshes
while visible every 30 seconds and hides expired evidence. API read failures
and unreachability take precedence over cached success.

This first slice assesses default-scope HTTP request and service workloads.
It excludes preview/dark releases, mirrors and one-off tasks from serving
capacity. Worker/job execution is explicitly unassessed. Active-instance scans
are bounded to 256 rows, deployment history to 50 recent rows, and collection
to 10 seconds; incomplete evidence is reported instead of inferred as zero.
`capacity.known` distinguishes a measured count from unavailable evidence.
The existing live-release read supplies serving revisions independently of the
bounded history window, so a failed recent release does not hide an older live
release. Raw deployment errors, probe output and infrastructure addresses are
not exposed.

This assessment does not prove public reachability, verify artifact existence,
probe dependencies, or provide an uptime guarantee. It describes evidence that
Gregale already has. Health history, automatic alerts, active public probes,
environment-specific request metrics and worker/job checks are future slices.
