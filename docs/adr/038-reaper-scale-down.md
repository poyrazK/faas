# ADR-038 — aggressive reaper scale-down (issue #171)

Status: Accepted, 2026-07-26. Owner: @poyrazK. Closes: #171.
Related: ADR-037 (reactive scale-up trigger, the matching ingress-side
signal path).

## Context

The current idle reaper (`pkg/sched/reaper.go::ReapIdle`) parks instances
one-by-one against each instance's per-plan `idle_timeout` (60/60/300/600 s
for Free/Hobby/Pro/Scale). For a 5-instance Pro app that served a 100 rps
burst and then dropped to 0, all 5 instances stay resident until the
**longest** idle cross the 300 s Pro timeout — burning `5 × (RAMMB + 8)` of
resident RAM after demand is zero. This breaks the financial model's
130 MB/sandbox average (§1, "fleet snapshot average target") and the
scale-to-zero pitch that is the platform's whole product.

Issue #171 acceptance gate: a 5-instance app that drops from 100 → 0 rps
must park back to `min_instances` within **30 s**, not 5 min. The
`min_instances` floor must still be honored. The existing `ReapIdle`
behavior must continue to pass its existing tests (Hobby/Free stay on
the timeout path — they don't pay for fast cooldown).

## Decision

Add a **second pure selector** `ReapAggressive(now, snapshot, desiredByApp) []string`
in `pkg/sched/reaper.go` that runs alongside `ReapIdle` on the same 10 s
reaper tick. It parks the **surplus** above `max(min_instances, desired + 1)`
when the recent RPS signal says we don't need them. The `+1` is a
hysteresis buffer so a brief RPS wobble does not wake-then-park on the
next request.

### Signal: per-app 5×1s rolling RPS

The signal comes from `gateway_requests_total` via a new independent
mirror package `pkg/sched/recentload/`. The mirror is **not** shared with
`pkg/sched/scaleup/`'s `RingBuffer` (which is private, optimized for
admission, and has different rollup semantics) — it is a small standalone
implementation fed by the same `scaleup.PromScraper` interface
(type-aliased, not duplicated). The mirror:

- maintains a 5 × 1 s ring per app (production) / 5 × 5 min (tests)
- reads cumulative counters from `gateway_requests_total{app=…}`
- folds the per-tick delta into the running bucket sum
- detects restart (cumulative dropped below lastSeen) by clearing the ring
- exposes `RecentRPS(appID, now) int64` and `RecentDesiredReplicas(appID, now, targetRPS) int`

The single `*HTTPPromScraper` is shared between `scaleup.Trigger` and
`recentload.RecentLoad` (one HTTP client, one connection pool, one set
of timeouts). Halves the conn churn; a future "single connection"
optimization is a one-line change instead of two.

### Target shape

`desired = ceil(windowed_rps / autoscale_target_rps)`. The
`autoscale_target_rps` column is the same one the scale-up trigger reads
at `pkg/sched/scaleup/trigger.go::Trigger.decide`. Single source of truth.

Apps absent from `desiredByApp` (no autoscale target set, or no signal
yet) fall through to `ReapIdle` unchanged. Single-instance apps
(`max_concurrency == 1`) are not in scope; the new path only fires for
multi-instance apps where surplus can exist.

### Loop wiring

`pkg/sched/loop.go` gains:

- a `recentLoad *recentload.RecentLoad` field
- a `WithRecentLoad(*recentload.RecentLoad)` builder (mirrors
  `WithScaleUp` at loop.go:151)
- a `runRecentLoad(ctx)` method called from a 1 s ticker (mirror of
  the scaleup ticker arm)
- a `runReaperAggressive(ctx, apps, snapshot, now)` method called
  between `ReapIdle` and `SelectEvictions` inside the existing
  `runReaper` 10 s tick
- a per-tick cap of 8 parks per app (`MaxParksPerTickPerApp`,
  `WithReaperParkCap`) to prevent a single tick from blocking the
  reaper for `8 × ~150 ms ≈ 1.2 s` during a sudden scale-down storm

`runReaper`'s `now := time.Now()` was changed to `now := l.now()` so the
test surface can drive a fake clock deterministically (the same pattern
already used by `runCronTick` at loop.go:913).

### Audit row

One events row per considered app per tick that parks ≥ 1 instance:

```
actor   = "schedd"
kind    = "reaper_scale_down"
subject = app_id (Postgres pointer)
data    = {
  "app":     app_id,
  "desired": int,
  "parked":  []string,
  "reason":  "traffic_dropped",
  "now":     RFC3339Nano
}
```

This is the same `AppendEvent(ctx, actor, kind, subject, data)` shape the
existing `state_transition` and `watchdog_timeout` audit rows use. The
`events.data jsonb` column already carries the audit payload — no
schema change.

### Metric

`schedd_scale_down_decisions_total{app, outcome}` where `outcome ∈ {park, keep}`,
added to the shared `wire.NewOpsMetrics("schedd")` registry. One
observation per app per tick that ran the new code path — symmetric with
the existing `schedd_scale_up_decisions_total` (ADR-037). The empty-app
labels are pre-instantiated so the panel exists at day 1.

### Feature flag

`FAAS_REAPER_AGGRESSIVE` (default ON, schedd.toml field
`reaper_aggressive`). When disabled, the mirror still runs so the metric
and audit row surface for diagnosis, but no parks happen. Cheap to add,
expensive to miss if a regression lands — the same pattern as
`FAAS_GRACE_INTERVAL` for §17 G6.

## Why this is the right shape

- **The reaper's `ReapIdle` is a pure function over `[]InstanceInfo`.**
  Keeping the new policy pure (`ReapAggressive`) preserves the
  testability property — every branch is reachable without a clock or
  DB. The full 30 s acceptance gate is pinned by a single property test
  on the engine (`TestProperty_EngineReaper_BurstToIdle30s`) that drives
  Loop.runReaper three times with the fake clock advanced 10 s per tick.
- **No new engine surface.** `Engine.Park` and `transitionWithKind`
  stay untouched. The memory note
  `schedd-engine-lock-narrowing.md` is explicit: "Do not apply the same
  pattern to `Park` / `Evict` / `snapshotAndPark` — their vmmd calls are
  short, reaper is 10 s apart." The aggressive path inherits that
  property by going through the same `Park` loop.
- **No SQL migration.** The audit row is one more value in the closed
  set of `events.kind` — the existing `data jsonb` column is the
  payload carrier.
- **No coupling to the scale-up `RingBuffer`.** Independent package,
  independent tests, independent metric. A regression in either
  direction is observable in isolation.

## Alternatives considered

- **Reuse `scaleup.RingBuffer` directly.** Rejected: it's a private type
  in `pkg/sched/scaleup/`, has different rollup semantics (per-instance
  RPS, not per-app aggregate), and the trigger already exposes a
  `DesiredReplicas` decision — a second consumer would couple two
  unrelated schedd subsystems to the same in-memory state.
- **Run the aggressive path on the same 10 s tick as `ReapIdle` but
  reuse the same `now` time and skip the mirror.** Rejected: the 10 s
  tick is too coarse for "30 s acceptance" — three ticks of slack is
  zero margin in a load-spike scenario. The 1 s mirror plus 10 s reaper
  gives a 30 s ceiling with margin (issue #171's hard requirement).
- **Emit one audit row per parked instance, not per app per tick.**
  Rejected: blows up the events table during a scale-down storm and
  loses the metric parity. One row per app per tick keeps the metric
  and audit views symmetric; the `parked` JSON array carries the
  individual instance IDs for forensic analysis.
- **Compute `desired` inline in `runReaper` instead of via
  `recentload.RecentDesiredReplicas`.** Rejected: the test surface for
  the pure function lives in the mirror package; threading the formula
  back into the loop would either duplicate the math or require a
  clock parameter that the loop doesn't have. The mirror's API is the
  seam.

## Consequences

- The `pkg/sched/loop.go` `now := time.Now()` change in `runReaper` is
  behaviorally identical (real `time.Now` and `l.now` returning
  `time.Now` produce the same value) but is a load-bearing seam for
  the integration test. Future reaper sub-ticks should use `l.now()`
  for the same reason.
- The aggressive path adds a Postgres read per app per tick (one
  `ListInstancesForApp` already in the snapshot loop, reused). No new
  queries; no measurable load shift.
- The metric `schedd_scale_down_decisions_total` is per-daemon
  (prefixed `schedd_` by `NewOpsMetrics`), so the spec §12 catalogue
  row reads exactly `schedd_scale_down_decisions_total{app, outcome}`
  with no daemon name in the metric name.
- Operators can disable in-place via `FAAS_REAPER_AGGRESSIVE=false` if
  a regression surfaces; the mirror + metric + audit row stay live so
  diagnosis continues.
- `pkg/sched/scaleup/` is not modified; the trigger and the mirror
  remain independent packages that share one `PromScraper` instance.

## Verification

- Pure unit: `ReapAggressive` 8 branches in `pkg/sched/reaper_test.go`
  (zero rps burst, `+1` buffer, G7 OpenConns protection,
  MinInstanceAge protection, single-instance, missing-app, floor=2 vs
  0, 3-instance scenarios).
- Wire: `TestOpsMetrics_ObserveScaleDown` in `pkg/wire/metrics_test.go`
  pins counter + pre-instantiated labels.
- Integration: 4 tests in `pkg/sched/loop_test.go`
  (`TestLoopReaperAggressive*`) cover the per-tick park path, the floor
  honored, the audit row emitted, the metric incremented.
- Property: `TestProperty_EngineReaper_BurstToIdle30s` in
  `pkg/sched/engine_test.go` pins the 30 s acceptance gate end-to-end
  via `fakeVMM` (per `invariants-property-test-fakevmm-reuse`).
- Build/lint: `make test` (fast), `make lint` (golangci-lint v2.4 +
  custom checks).
- Live smoke: `curl http://localhost:8080/metrics | grep
  schedd_scale_down_decisions_total` shows `outcome="park"` and
  `outcome="keep"` rows after the box has run a scale-down cycle.
