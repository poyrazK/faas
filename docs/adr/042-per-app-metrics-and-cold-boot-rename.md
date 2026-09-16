# ADR-042 — per-app request metrics + cold-boot rename (issue #273)

Status: Accepted, 2026-07-28. Owner: @poyrazK. Closes: #273.
Related: ADR-036 (instance metrics cardinality rollups — the
precedent for "roll up at the edge, not in Prometheus"); ADR-029
(M8 dashboard — the dashboard surface this lands on); ADR-040
(per-account rate limit — same `__other__` placeholder precedent,
unrelated in concern).

- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Partially superseded (in §1 only, ADR-093):** opt-in apps with
  `apps.route_metrics_enabled=true` additionally emit
  `gateway_request_duration_seconds{app,route,class}` and an
  in-memory control-listener reader at
  `GET /v1/internal/apps/{slug}/routes`. The decision to drop the
  `route` label for **non-opt-in** apps is preserved verbatim. The
  `{app, class}` histogram and the `cold_wake` → `cold_boot` rename
  are untouched. See ADR-093 for the bounded-per-app opt-in shape.

## Context

Customers cannot see how their own app is performing. The only
latency signal on the box, `gateway_wake_latency_seconds`
(`pkg/gateway/metrics.go:77-84`), is a **fleet-wide unlabeled
histogram** — it answers "is the platform waking apps fast?" but
never "is *my* app slow?". `gateway_requests_total{app,plan,code}`
counts requests per app but carries no timing, so a customer cannot
distinguish platform wake time from their own execution time, or
verify that a deploy improved anything.

Issue #273 names eight acceptance criteria; exploration against the
code turned up three facts that shape the implementation, two of
which deviate from the criteria.

## Decisions

### 1. Drop the `route` label (criterion #1 / #2 deviation)

The issue's metric shape `gateway_request_duration_seconds{route,
app}` assumes gatewayd reads `r.URL.Path`. It does not — gatewayd is
an opaque reverse proxy and never inspects the request path. Adding
the label would require introducing a route concept from scratch,
which is well outside the issue's scope.

**Precedent (ADR-036)**: per-instance detail lives in the in-memory
`pkg/sched/instancestats.Reader` while Prometheus gets bounded
rollups, because "per-instance Prometheus cardinality is unbounded
under the §6.2 fan-out invariant" (spec §12). Customer URL paths are
strictly worse than instance ids: unbounded *and* attacker-
influenced. Dropping the label removes an entire class of
normalisation bug rather than trying to bound it.

**Histogram emits `gateway_request_duration_seconds{app, class}`**
where `class ∈ {2xx, 3xx, 4xx, 5xx}` (derived by the new
`statusClassBucket(status int) string` helper). The existing
`statusClass()` (which returns the full 3-digit code) is unchanged —
it still feeds the counter `gateway_requests_total{app, plan, code}`
so dashboards can drill into e.g. 404 vs 503.

### 2. Cardinality math (closed-set vs full code)

A Prometheus histogram with N buckets emits N + `_sum` + `_count`
series per label combination — 3 exposition rows per combo. With
the 11 chosen buckets (`{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
1, 2, 5, 10}`) that's `11 + 3 = 14` series per (app, class) combo:

| Label set | series/app | at 1,000 apps |
|---|---|---|
| `{app, plan, code}` full code (~60 realistic values) | ~840 | ~840,000 ✗ |
| `{app, class}` class ∈ 4 values | 14 × 4 = **56** | **~56,000** ✓ |

Per-app pre-instantiation of the closed `class` set (via the new
`Metrics.PreInstantiateApp(appID)` helper, deduped on the Handler
via `sync.Map`) keeps the dashboard panel present from the first
request, not the first observation.

The customer metrics API uses the histogram's `_count` series for both its
request denominator and its 4xx/5xx numerator. These counters are written by
the same `Handler.observe` call as durable request telemetry, and the closed
class series are pre-instantiated. A full-code `gateway_requests_total` series
is created only after a particular status first occurs; if a new 504 series is
created between scrapes, Prometheus cannot reconstruct that first increment.
The full-code counter remains available for operator drill-down, while the
closed-class population provides accurate customer totals and error rates.

`AppMetricsResponse.as_of` reports the latest Prometheus scrape timestamp when
that timestamp is available. Consumers can therefore account for the normal
scrape delay when comparing this projection with the durable analytics ledger.

### 3. `gateway_cold_wake_total` → `gateway_cold_boot_total` rename (criterion #3)

The metric has zero external consumers:

- No `deploy/grafana/*.json` panel references it (both copies are
  byte-identical and use `gateway_wake_latency_seconds` only).
- `faas.rules.yml` has no alert or record rule on it.
- No runbook cites it.
- No ADR or spec section references it.

The existing test in `pkg/gateway/handler_test.go:560` reads the Go
field (`h.metrics.coldWake`), not the exposition string — it would
have passed a rename silently. The new
`TestMetricsIssue273Exposition` reads the scrape body and asserts
the new name is present *and* the old name's series line is absent.
This is the same exposition-string pattern as
`TestMetricsWakeQueueWaitRegisters` (the only test that catches a
silent rename of `gateway_wake_queue_wait_seconds`).

**No dual-emit migration.** Dual-emit would have doubled the
observed cold-boot rate for the overlap window and added two
counters' worth of dashboard confusion for no benefit. The rename
is straight: Go field, exposition name, and the Help text all
switch together.

### 4. Criterion #4 — pre-instantiation scope is honest

App IDs are runtime values; they cannot be pre-instantiated at boot
(this would mint N×4 series for N apps, breaking the §12
cardinality budget before the first request). What this PR does
deliver:

- The closed `class` set per app is pre-instantiated at first
  `Backend.Lookup` hit (via `Metrics.PreInstantiateApp` + the
  `preInstantiateApps` sync.Map dedupe on the Handler).
- The existing closed-label pre-instantiation precedents (TLS
  on-demand denial reasons, ADR-024 H3; per-account rate limit
  plans, ADR-040) continue to be the model.

This is what the criteria ask for in practice; the literal "every
label tuple for every app on boot" reading is rejected because it
would mint unbounded series. The ADR documents the deviation
rather than implying a stricter invariant than the code can hold.

### 5. Cold-boot semantics

`gateway_cold_boot_total` (and its predecessor) increments on the
**leader** of a WakeGate coalescing burst: the request that
actually triggered `Backend.Admit`. Followers that park on the
gate's `Wait` see `cold=false` even though they waited
(`pkg/gateway/handler.go:492-561`). So `cold_start_pct` is "share
of requests that triggered a boot", not "share of requests that
hit a parked app" — both useful, but distinct.

`gateway_wake_queue_wait_seconds` (the wake-queue histogram, ADR
+ fleet dashboard) is where followers' wait shows up. The dashboard
copy makes this explicit so a customer reading "cold start 8%"
doesn't infer the wrong thing.

### 6. Cold-boot-vs-TTFB and the production forwarder

The new histogram measures **full request duration** (request-
received → handler-return). It does **not** depend on the first-
upstream-byte stamp, so it sidesteps a pre-existing bug:

The production forwarder is `pkg/gateway/forwardproxy.go::ForwardingReverseProxy`
(`cmd/gatewayd/main.go:363` wires `handler.WithForwarding(deps.nodeCache.Forwarding())`),
which is a **buffered unary gRPC call** to `vmmdpb.ForwardHTTPRequest`.
No `httptrace.ClientTrace`, no streaming — "first upstream byte"
does not exist on that path. The existing `gateway_wake_latency_seconds`
takes the `time.Now()` fallback (`handler.go:388-394`) in
production, meaning the fleet wake histogram is wrong today.

This PR does **not** fix that bug — fixing it requires streaming
the forward RPC or stamping on the vmmd side. A separate issue
will track it. The new `gateway_request_duration_seconds` is the
full duration, so #273 is unaffected by the latent bug.

### 7. WithStartTime dead-code fix

`pkg/gateway/observability.go:59` defines `WithStartTime` but
nothing in the repo calls it. `startTime(r)` therefore always fell
back to `time.Now()`, making the slog `latency_ms` field effectively
~0 and any total-duration measurement meaningless. The fix is a
one-line context write at the top of `ServeHTTP` that everyone
already uses through `startTime(r)`. This is a real bug fix that
unblocks the new histogram; called out in the PR body so the
scope creep reads as deliberate rather than opportunistic.

## What this PR adds

- `pkg/gateway/metrics.go`: new
  `gateway_request_duration_seconds{app, class}` histogram,
  `Metrics.ObserveRequestDuration(appID, class, d)` (nil-safe),
  `Metrics.PreInstantiateApp(appID)`; renamed `coldWake` →
  `coldBoot`, `gateway_cold_wake_total` → `gateway_cold_boot_total`,
  `ObserveColdWake` → `ObserveColdBoot`.
- `pkg/gateway/handler.go`: wires `WithStartTime` at request entry,
  calls `PreInstantiateApp` on every `Backend.Lookup` hit (deduped),
  feeds duration into the new histogram through the single exit
  funnel `observe()` (no new call sites).
- `pkg/promql/client.go`: extracted from `cmd/apid/status.go` with
  an `HTTPDoer` seam so `cmd/apid/status.go` (status page) and
  `cmd/apid/handlers_metrics.go` (per-app) share one tested
  transport.
- `pkg/api/dto.go` + `sdk/go/internal/api/dto.go`:
  `AppMetricsResponse` with `x-since: "2026-07"` and
  `x-issue: "273"` on every field.
- `pkg/api/client.go`: `Client.GetAppMetrics(ctx, slug, rng)`.
- `cmd/apid/handlers_metrics.go`: `GET /v1/apps/{slug}/metrics?range=`
  handler, IDOR-safe via `loadApp`, scoped to `api.ScopesReadSurface`,
  no `requireMFA`. Closed range vocabulary
  `{5m, 15m, 1h, 6h, 24h, 7d, 15d}` bounded by Prometheus
  retention.
- `pkg/dashboard/dashboard.go` + `pkg/dashboard/templates/app_detail.html`:
  new Metrics section with `<dl>` rows, empty state on
  `request_count==0`, distinct wake vs request latency rows,
  nonce-tagged style.
- `api/openapi.yaml` + `pkg/apid/openapi.yaml`: route + schema with
  `x-since: "2026-07"` and `x-issue: "273"`.

## What this PR does NOT add

- No Grafana panel changes. The new histogram's first consumer is
  the customer dashboard; the §12 fleet dashboard does not need a
  new panel because it already exposes the per-app counters.
- No alert rule changes. `gateway_cold_boot_total` does not need a
  new alert — fleet wake latency is the existing SLO signal.
- No new runbook. The `cold` semantics caveat lives in this ADR
  and the dashboard copy.
- **No dashboard range selector (follow-up).** The
  `GET /v1/apps/{slug}/metrics?range=` API endpoint accepts the
  closed 7-set vocabulary; the dashboard page currently hard-codes
  `5m` in `fetchDashboardMetrics` rather than reading `?range=` and
  rendering a `<form method="GET">` picker. Plumbing a range param
  through `renderAppDetail` → `fetchDashboardMetrics` and adding
  the picker is a 30-line follow-up that lives in this PR's
  backlog. The API is the source of truth — the dashboard form is
  a UI nicety.

## Deviations from #273 acceptance criteria

| Criterion | What was asked | What we ship | Why |
|---|---|---|---|
| #1, #2 | histogram with `route` label | histogram with `class` label | gatewayd has no route concept; ADR-036 precedent |
| #3 | migration plan | straight rename + exposition-string test | zero external consumers; dual-emit doubles the rate |
| #4 | pre-instantiate all labels | pre-instantiate closed `class` per app | app IDs are runtime values |

## References

- Issue #273 — original request
- ADR-036 — instance metrics cardinality rollups (precedent)
- ADR-029 — M8 dashboard (the surface this lands on)
- ADR-040 — per-account rate limit (closed-set pre-instantiation precedent)
- Spec §12 — Prometheus dashboards
- Spec §4.1 — gatewayd rate-limit contract (unchanged)
- Spec §11 — abuse-vector observability (unchanged)
