# ADR-097 · schedd wake-phase telemetry

- **Status:** accepted v1.0 (2026-08-12; renumbered 093 → 096 → 097 to avoid
  collisions with ADR-093 (per-route-app-metrics, issue #273 / PR #861) and
  ADR-096 (customer-facing error grouping, PR #863 PR-A — reserves the slot
  for PR-C's `docs/adr/096-customer-error-grouping.md`))
- **Date:** 2026-08-12
- **Decision:** Add a single new Prometheus HistogramVec
  `schedd_wake_rpc_duration_seconds{app, phase}` on schedd, with closed-set
  `phase ∈ {admit_to_rpc, rpc_call, rpc_to_running}`. Bucket set reuses the
  spec §6.3 wake-latency budget (`0.05`–`5` seconds) with one extra low-end
  bucket (`0.01`) for the admit-to-RPC phase, which is dominated by
  lock-acquire + ledger consult and rarely exceeds a few milliseconds. Each
  observation attaches `wake_id` as a `prometheus.Exemplar` so operators can
  join to the existing `events` table (`pkg/sched/events.go`) and to the
  gateway-side `gateway_wake_latency_seconds` (pkg/gateway/metrics.go:335-342)
  on the same wake event. No new wire surface, no DB migration.
- **Why:** The platform snapshot-wake SLO (spec §6.3: p95 < 350 ms through RUNNING;
  dashboard at docs/faas_implementation_spec.md:755) is measured today at
  **one** point — the gateway's first upstream byte
  (`pkg/gateway/handler.go:3010`). When p95 regresses, an operator has no way
  to attribute the regression to a specific schedd phase: the admit-gate
  consult (`pkg/sched/engine.go:4845`–`4890`), the placement chooser
  (`:1357`), the vmmd RPC itself (`:1744` and `:1759`), or the WAKING →
  RUNNING transition (`:1892`). The schedd-side wake path emits exactly one
  `time.Now()` capture (`startedAt` at engine.go:1570) and zero duration
  histograms. PR-C (issue #462) introduced `schedd_guest_init_duration_seconds`
  for the in-VM guest-init slice, but that name covers only what happens
  inside the VM after the vmmd RPC returns, not the schedd-side phases that
  bracket it. Without phase decomposition, the wake-latency SLO is a black
  box and capacity regressions are triage-by-instrumented-guess.

  The split is: (1) **`admit_to_rpc`** measures the schedd-side internal
  work between the gRPC handler receiving the wake and the vmmd RPC
  starting — lock acquire (`engine.go:1193`), `resolveApp`, `admitGate`,
  placement (`engine.go:1357`), `store.CreateInstance` (`:1387`),
  `ledger.Admit` (`:1394`), wake_id mint (`:1301`). (2) **`rpc_call`**
  measures the vmmd dial + `CreateFromSnapshot` or `CreateColdBoot` round
  trip — the only phase that crosses a process boundary. (3)
  **`rpc_to_running`** measures the schedd-side commit work between the
  vmmd RPC returning nil and the WAKING/COLD_BOOTING → RUNNING transition —
  state-machine emit, instance row update, watchdog bookkeeping.

  Buckets reuse the §6.3 set because that's the budget operators
  already think in. The extra `0.01` bucket on the low end reflects
  that `admit_to_rpc` is dominated by in-process work and rarely
  exceeds a few ms — it would otherwise collapse to the `0.05` bucket
  and lose observability on the lock/ledger path.

- **Consequences:**
  - **New metric, additive only.** `pkg/wire/metrics.go` gets a new
    `wakePhaseDuration` `HistogramVec` with labels `{app, phase}` and the
    bucket set above. Closed-set pre-instantiation mirrors the existing
    precedent at metrics.go:1172-1177 (`guestInitDuration`), metrics.go:2359
    (scale-up outcome), metrics.go:2388-2396 (scale-down outcome),
    metrics.go:2401-2406 (floor reconciler outcome): the empty-app label is
    pre-instantiated for every phase value so the help/TYPE surfaces in
    `/metrics` from boot. Per-app series count grows by 3 (one per phase
    value); this is the same precedent as `guestInitDuration{app, runner}`
    on schedd today.
  - **Three new instrumentation sites.** Engine.go:1570 keeps the existing
    `startedAt := time.Now().UTC()` and adds `rpcStartedAt := time.Now().UTC()`
    immediately before `vmm.CreateFromSnapshot` (`:1744`) and
    `vmm.CreateColdBoot` (`:1759`). After each call returns nil,
    `rpcEndedAt := time.Now()` is captured. The defer-observe pattern is
    NOT used for the `rpc_call` observation — the early-return error
    branches at engine.go:1762-1790 already record their duration via the
    `events.BootFailed{FailedAt: …} - events.BootStarted{StartedAt: …}`
    math, so we observe on the success path only and avoid double-counting
    error duration in two metrics. After the WAKING/COLD_BOOTING → RUNNING
    transition at engine.go:1892, observe `rpc_to_running`. Each
    observation lifts `bootInput.wakeID` as an `exemplar.Add(traceID,
    "wake_id", wakeID)` so operators can `click` a histogram bucket and
    land on the events row for the same wake.
  - **Exemplar support.** The `prometheus.DefaultRegisterer` used by
    `pkg/wire.NewOpsMetrics` already supports exemplars since
    `client_golang` 1.x. No registry changes needed. If the exemplar
    observer is not wired in a given process (e.g. test binaries), the
    exemplar is silently dropped — the histogram count is unaffected.
  - **Cardinality.** Per schedd, `O(autoscale-enabled apps × 3)` new
    series. Comparable to the existing
    `schedd_guest_init_duration_seconds{app, runner}` precedent. Within
    the spec §12 budget.
  - **Spec text update.** Spec §6.3 currently lists the 5-phase budget
    decomposition (gateway route / netns/TAP/jailer / snapshot load /
    guest resume / proxy first byte) without pointing at any per-phase
    metric. Add a paragraph naming the new histogram and stating which
    schedd phase corresponds to which histogram row. Spec §12.1 (the
    metric catalogue at line 788) gets a new table row for
    `schedd_wake_rpc_duration_seconds`.
  - **No behaviour change.** All three observations are
    `prometheus.Observer.Observe(seconds)` calls — pure telemetry, no
    conditional branches on the value. The new `time.Now()` calls are
    constant overhead (< 1 µs each, three per wake).
  - **No new ADR sibling.** This is a metric-surface ADR only. The wake
    state-machine, admit-gate, ledger, and vmmd RPC shapes are unchanged.

## Reused primitives (no change)

- **Spec §6.3** (`docs/faas_implementation_spec.md:616-625`) — the 5-phase
  budget decomposition this ADR decomposes on the schedd side. The new
  histograms observe three schedd phases; the gateway-side
  `gateway_wake_latency_seconds` observes the end-to-end sum.
- **`schedd_guest_init_duration_seconds`** (`pkg/wire/metrics.go:1172-1177`) —
  the HistogramVec pattern + nil-safe `ObserveGuestInit` accessor + closed-set
  pre-instantiation. The new vector mirrors this shape exactly.
- **`prometheus.Exemplar`** — available on `prometheus.DefaultRegisterer`
  since `client_golang` 1.x. No new dependency.
- **Wake_id correlation handle** — `bootInput.wakeID` is already minted
  (engine.go:1301 UUIDv7), propagated to vmmd, persisted on the instance row,
  logged, and returned in `WakeResult.WakeID` (engine.go:1923 → scheddgrpc
  response header `x-faas-wake-id`). The events table
  (`pkg/sched/events.go`, `BootStarted` / `BootCompleted` rows) already keys
  on the same wake_id. The exemplar attaches to the histogram bucket
  without adding a label.

## Per-PR surface

This ADR is implemented as a single commit (P1B) inside the broader
P1 autoscaling PR-cluster alongside P1A and P1C. The P1B commit
introduces:

- **`pkg/wire/metrics.go`** — new `wakePhaseDuration` HistogramVec,
  field on `OpsMetrics` struct, nil-safe `WakePhaseDuration(app, phase
  string) prometheus.Observer` accessor. Pre-instantiates empty-app ×
  3 phase rows in the constructor's closed-set section (next to the
  existing scale-up / scale-down / floor loops at metrics.go:2352-2406).
- **`pkg/sched/engine.go`** — three new `time.Now()` captures and three
  new histogram observations (one per phase). All sites are inside the
  existing `Engine.Wake` / `Engine.AdmitInstance` paths; no new
  exported functions.
- **`pkg/wire/metrics_test.go`** — `TestOpsMetrics_ObserveWakePhase`
  (mirrors `TestOpsMetrics_GuestInitDuration` at line 1153) and
  `TestOpsMetrics_ObserveWakePhaseNilSafe`.
- **`pkg/sched/engine_test.go`** — `TestEngineWake_PhaseHistograms_Recorded`
  using the existing `fakeVMM{sleepFor: …}` fixture
  (`engine_test.go:52-53`) with a 50 ms sleep, asserting non-zero
  observation count for all three phases after a single wake.
- **`docs/faas_implementation_spec.md`** — §6.3 paragraph + §12.1 row.
- **`docs/adr/097-schedd-wake-phase-telemetry.md`** — this file.

## Future work

- **Watchdog kill duration metric.** `Watchdog.KillStuck`
  (`pkg/sched/engine.go:4313-4377`) does NOT emit a duration metric on
  kill — only `schedd_watchdog_kills_total{from_state, to_state}`. Adding
  `schedd_watchdog_kill_latency_seconds{kind}` would complete the
  observability story for stuck-instance latency. Separate ADR.
- **Predictive phase budgets.** Once we have a few weeks of `phase`
  histogram data in production, we can tighten §6.3's budget per phase
  rather than as a single end-to-end number. Out of scope for this ADR.
- **Exemplar→trace stitching.** If OTel trace exporters are wired in
  a future slice, the exemplar `wake_id` should join to the
  `pkg/sched/vmmrouter.go` OTel span at `engine.go:1743` /
  `:1758`. Today the exemplar is just a label; the trace bridge is
  follow-up.

## Rejected alternatives

- **Three separate metrics instead of one vector.** Rejected — three
  metrics with the same bucket set and overlapping labels produces three
  near-identical dashboard rows; a single vector with a `phase` label
  keeps the dashboard contract clean (one panel, three sub-panels by
  phase) and matches the existing `schedd_guest_init_duration_seconds{app,
  runner}` pattern.
- **Extend `schedd_guest_init_duration_seconds` with a `phase` label.**
  Rejected — the existing metric covers only what happens inside the VM
  after the vmmd RPC returns (the `guest-init` time measured by
  guest-init's own timing). Repurposing the name would be semantically
  wrong; reviewers and operators reading the dashboard would conflate
  "guest init inside the VM" with "schedd-side admit and RPC". The new
  vector has a distinct name and Help text.
- **Add `wake_id` as a regular metric label.** Rejected — per-wake-id
  labels multiply cardinality by `O(autoscale-enabled apps × wakes/sec)`,
  which is unbounded. Exemplars attach a sparse sample of labels to
  existing buckets without increasing cardinality.
- **Use OpenTelemetry span buckets instead of a Prometheus histogram.**
  Rejected — Prometheus is the source-of-truth for the SLO dashboards
  (`docs/faas_implementation_spec.md:755`) and operators' muscle memory
  is on `gateway_wake_latency_seconds`. Adding a parallel OTel-only
  surface would fragment observability.
- **Defer to a separate post-PR-cluster.** Rejected — the wake-latency
  SLO is the headline product metric and operators need phase-level
  observability to triage the next regression. Shipping alongside P1A
  and P1C keeps the autoscaling-observability improvements in one
  reviewable unit.
