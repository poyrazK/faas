# ADR-030 · builderd build metrics + real build-success SLO

- **Status:** accepted
- **Date:** 2026-07-23
- **Decision:** builderd emits its build lifecycle on the shared
  `pkg/wire.OpsMetrics` bag it already constructs (but previously threw
  away). Three series carry the build signal:
  - `builderd_ops_total{op="build",code}` — the reused ADR-015 counter.
    `code ∈ {ok, cache_hit, oom, timeout, user_error, infra}` (the last
    four are the `state.FailureClass` string values verbatim).
  - `builderd_build_duration_seconds` — a new histogram labelled by
    `outcome ∈ {cache_hit, ok, failed}`, buckets `{5, 15, 30, 60, 120, 240,
    360, 480, 600}` (seconds). The label lets the §12 panels slice out
    cache hits (< 1 s) from real-build latency — without it the fleet
    p50/p95 is dominated by cache noise.
  - `builderd_build_queue_wait_seconds` — a new **unlabelled** histogram,
    buckets `{1, 5, 15, 30, 60, 120, 300, 600}` (seconds).

  The public status page's build-success SLO (`cmd/apid/status.go`) is
  repointed from `vmmd_op_duration_seconds{op="create_cold_boot"}` to
  `sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m])) /
  sum(rate(builderd_ops_total{op="build"}[5m])) * 100`.
- **Why:** Spec §12 names two build dashboard rows — *build success* (SLO:
  ≥ 99 % non-`user_error`) and *build queue wait p95* (< 60 s, warn >
  300 s) — that no daemon emitted. `cmd/builderd/main.go` constructed
  `wire.NewOpsMetrics("builderd")` **inline just to call `.Handler()`** and
  discarded the object, so nothing was ever observed on it. Worse, the
  status page derived "build success" from vmmd's **cold-boot** op — a
  wake-path signal, not a build-path one — publishing a wrong public
  number. This ADR makes the build series real and fixes the SLO source.
- **Consequences:**
  - `pkg/builderd.Builderd` gains an `ops *wire.OpsMetrics` field and a
    `WithOpsMetrics(ops)` setter (mirrors `pkg/sched.Engine.WithOpsMetrics`,
    `cmd/schedd/main.go:196`). `cmd/builderd` builds one OpsMetrics, wires
    it into the orchestrator, and mounts the same object at `/metrics`.
  - `ProcessOne` observes queue-wait once (right after mark-running, so
    only builds that actually dequeued count) and build duration via a
    `defer` (covering every terminal return — success, cache hit, failure
    — exactly once). The deferred observe reads a `durationOutcome`
    variable set by the `markSucceeded`/`markFailed` funnels (the
    single choke points for every terminal path): `markSucceeded` sets it
    to the same `code` it passes to the counter (`ok` or `cache_hit`);
    `markFailed` sets it to `"failed"`. The `ops_total{op="build",code}`
    counter is incremented in the same funnels. `markSucceeded` grew a
    `code` arg to distinguish `ok` from `cache_hit`.
  - All observer methods (`ObserveBuildCount`, `ObserveBuildDuration`,
    `ObserveBuildQueueWait`) are **nil-safe** so the many builderd unit
    tests that call `New(...)` without metrics keep working unchanged.
  - Queue wait needs an enqueue timestamp the `builds` table lacked.
    Migration `00027_builds_enqueued_at.sql` appends
    `enqueued_at timestamptz not null default now()` (the default
    backfills existing rows; apid's insert relies on it). `state.Build`
    grows `EnqueuedAt`; `scanBuild` + the three build selects in
    `pgstore.go` and `MemStore.CreateBuild` are updated.
  - Grafana `faas-fleet.json` gains two panels (build success gauge,
    queue-wait p95 timeseries) with §12 thresholds baked in.
- **Rejected alternatives:**
  - **Reuse `OpsMetrics.ObserveCode("build", …)` for duration.** That
    method also feeds the control-plane `op_duration_seconds` histogram,
    whose buckets top out at 5 s — useless for a build that runs up to the
    600 s cap. Dedicated build-sized histograms are the ADR-027 precedent
    (`stripe_push_duration_seconds` exists for exactly this reason).
  - **Label the duration/queue-wait histograms by framework or plan.**
    Multiplies cardinality without making the §12 panels (which aggregate
    fleet-wide) more actionable. The counter's `code` label already
    carries the only cut the SLO needs. (We DID add a 3-value `outcome`
    label on the duration histogram — see below — because cache hits run
    sub-second and would otherwise drown real-build latency in the p95
    panel. Cardinality stays bounded at 3 × 9 buckets = 27 series.)
  - **Keep the vmmd cold-boot proxy for the SLO.** It measures wake
    success, not build success — a category error on a public status page.
  - **New frozen metric names `builderd_build_total{result}`.** Rejected
    per user decision: reuse the ADR-015 `ops_total{op,code}` shape so no
    new counter name enters the frozen set; only the two timing histograms
    are new (and they follow the ADR-027 naming precedent).

## Metric names (final, ADR-030)

| Name | Type | Labels |
|---|---|---|
| `builderd_ops_total` | counter | `op="build"`, `code ∈ {ok, cache_hit, oom, timeout, user_error, infra}` |
| `builderd_build_duration_seconds` | histogram | `outcome ∈ {cache_hit, ok, failed}` |
| `builderd_build_queue_wait_seconds` | histogram | none |

## Cross-reference

- `pkg/wire/metrics.go` — the two histograms + `ObserveBuild{Count,Duration,QueueWait}`.
- `pkg/builderd/builderd.go` — `ops` field, `WithOpsMetrics`, observation sites.
- `cmd/builderd/main.go` — single OpsMetrics wired into orchestrator + `/metrics`.
- `migrations/00027_builds_enqueued_at.sql`, `pkg/state/{types,pgstore,memstore}.go`.
- `cmd/apid/status.go` — repointed build-success PromQL.
- `deploy/grafana/faas-fleet.json` — the two new panels.
