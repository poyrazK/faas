# ADR-194 · Multi-signal scaling targets

- **Status:** accepted
- **Date:** 2026-09-21

## Context

An app declares how it scales through `scaling.target`, a single
`{metric, value}` pair. The closed metric set is asserted in three places —
`pkg/gregalemanifest/manifest.go` (manifest validation),
`cmd/apid/handlers_ext.go` (PATCH validation) and `pkg/deploydiff/quota.go`
(plan gating) — and all three agree it is
`rps | concurrent_requests | queue_depth | p99_latency_ms`.

Two of those four metrics do nothing.

`pkg/sched/targets`, the trigger that consumes `ScalingPolicy.Target`, opens
its per-app branch with an explicit filter:

```go
metric := policy.Target.Metric
if metric != "concurrent_requests" && metric != "queue_depth" {
    // Other metric axes (rps / cpu) are handled by pkg/sched/scaleup.
    continue
}
```

That comment is true of the axes but not of the surface. `pkg/sched/scaleup`
never reads `ScalingPolicy` at all; it reads the legacy integer columns
`apps.autoscale_target_rps` and `apps.autoscale_target_cpu_pct`:

```go
if app.AutoscaleTargetRPS == 0 && app.AutoscaleTargetCPUPct == 0 {
    continue
}
```

So `target: {metric: rps, value: 50}` validates, persists to
`apps.scaling_policy`, round-trips through the API, renders in `deploy diff`,
and is read by nothing. `p99_latency_ms` is worse: no trigger implements a
latency axis at all, and no component computes a per-app p99 for scaling.
Both are contracts the platform advertises and does not honour — the exact
failure mode this repo has now hit twice (`FAAS_FUNCTION_RUNNER_*` never wired,
`FAAS_GATEWAY_STREAMING` never set by any deploy path).

The second problem is that a single `target` cannot express what real
workloads need. A queue-backed worker that also serves HTTP has two demand
signals. An app whose requests are cheap but CPU-bound has a request-rate
signal that under-provisions and a CPU signal that catches it. Today the
developer picks one and loses the other — or splits the app.

The obvious fix is the one Kubernetes made: let the developer list metrics,
each with a target, and let the platform take the max. The obvious trap is
the one Kubernetes also made: stabilization windows, tolerance, per-metric
behaviour policies, scale-up and scale-down rules with `selectPolicy` — an
autoscaling algorithm the developer must design. Gregale's premise is that
the platform owns the algorithm.

## Decision

`scaling.targets` is a **list** of `{metric, value}`. The platform evaluates
every declared target independently and provisions for the **maximum** desired
replica count. Everything else about the decision — the windowing, the burst
bound, the cooldowns, the plan cap, the per-node RAM ceiling — stays platform
policy and is not exposed per metric.

```yaml
scaling:
  min_instances: 1
  max_instances: 20
  targets:
    - metric: concurrent_requests
      value: 80
    - metric: cpu
      value: 70
```

The singular `target:` remains accepted and is promoted to a one-element list,
so every existing manifest and every stored `scaling_policy` row keeps its
current meaning. `scaling_policy` is `jsonb`, so this needs no migration.

### The metric set is exactly the set that is implemented

| metric | source | trigger |
|---|---|---|
| `rps` | `RequestRateReader` / gateway scrape | `pkg/sched/scaleup` |
| `cpu` | `InstatsReader.MaxCPU` | `pkg/sched/scaleup` |
| `concurrent_requests` | `InstatsReader.MaxInflightForApp` | `pkg/sched/targets` |
| `queue_depth` | queue stats / bindings | `pkg/sched/targets` |

`cpu` becomes declarable for the first time — it was reachable only through
the legacy column, which no manifest field writes.

`p99_latency_ms` is **rejected on write**. It has no source and never had one;
accepting it only lets an operator believe latency-driven scaling is
configured. Stored rows that already carry it are inert in exactly the way
they have always been inert, and the rejection names the replacement:
`concurrent_requests` is the signal a latency target is actually reaching
for, since queueing is what makes p99 rise on a saturated instance.

### One arbiter, two triggers

`pkg/sched/scalesignal` holds the arbitration and nothing else. It is a leaf
package — it imports `math` and nothing from `pkg/sched` — because `targets`
and `scaleup` already cannot import each other (their doc comments record the
cycle) and a shared arbiter must not reintroduce one.

The arbiter is pure:

```go
func Desired(concurrency int, obs []Observation) int
```

Each `Observation` is a declared target plus its measured value; the arbiter
converts each to a desired count and returns the max. Per-metric conversion is
deliberately not uniform, and this is the part that must not be "simplified":

- **Rate metrics** (`rps`, `concurrent_requests`, `queue_depth`) are capacity
  measurements. Total demand divided by the per-instance target gives a
  desired count directly: `ceil(measured × concurrency / target)`.
- **Saturation metrics** (`cpu`) are not. A 90% CPU reading does not mean
  "provision 90/70 instances"; CPU has no linear relationship to capacity
  once a runtime starts contending. CPU therefore contributes exactly
  `concurrency + 1` when hot — a step, not a ratio. This preserves the
  behaviour `scaleup.decide` already had and keeps a CPU target from
  multiplying the fleet off a single noisy sample.

Because each trigger can only observe its own subset, each arbitrates over
the targets it can read. They are separate `case` arms of one `select` in the
schedd loop, so they serialize and cannot double-admit: whichever runs first
admits, and the second sees the raised `Concurrency` and arbitrates against
it. The net effect across a tick pair is the max over all signals, which is
the contract. A single global trigger reading every source would be a larger
change for the same result.

### Precedence with the legacy columns

`targets` entries win. When no `targets` entry names `rps` (or `cpu`), the
corresponding legacy column is used for that axis. An app that sets neither
behaves exactly as it does today.

## Consequences

A developer declares the signals their workload actually has and never writes
a scaling rule. `scale on concurrency 80 and cpu 70%` is two lines; the
combination logic is the platform's.

The closed metric set is now enforceable as "implemented", and
`TestEveryDeclaredMetricHasASource` fails the build if a metric is added to
validation without a trigger reading it. That test is the real deliverable
here: it is what makes a third phantom impossible rather than merely unlikely.

`p99_latency_ms` becomes a 422 on write. Nothing observable changes for a
running app, because nothing ever read it.

Not decided here, and deliberately out of scope: Kafka lag, custom
application metrics, and schedule-based scaling. Each needs a source that does
not exist yet (a consumer-group reader, a push or scrape path for app-defined
series, a cron evaluator against `min_instances`), and each can be added as a
new metric in this list without changing the arbitration contract — which is
the point of fixing the contract first.
