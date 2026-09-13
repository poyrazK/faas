# ADR-082 · Per-app customer-facing SLO surface (issue #696 / move-2 PR-A)

- **Status:** accepted
- **Date:** 2026-08-07
- **Issue:** #696
- **Supersedes:** none
- **Related:** ADR-042 (issue #273 / GetAppMetrics), issue #393 (account-scoped /v1/apps/metrics)

## Context

The existing observability surface (`GET /v1/apps/{slug}/metrics` and
`GET /v1/apps/metrics`, issues #273 / ADR-042 and #393) is a
**5m-window dashboard panel**: current request count, latency p50/p95/p99,
error rate, cold-boot rate, and fleet wake p95. Customers can read it
in the browser at `/dashboard/apps/{slug}` today.

It does NOT answer the customer-facing SLO questions that AWS
CloudWatch per-function and GCP Cloud Run per-service answer:

- "What is my p95 latency over the last 1h / 24h / 7d?" (SLO window).
- "What is my cold-boot rate over the last 24h?"
- "What is my error rate over the last 7d?"
- "How many GB-hours / instance-hours did I use?" (co-located with the
  latency/error/cold panel; today this is in `/v1/usage/summary`).
- "What is my wake-queue p95?" (visibility into the queueing introduced
  by the per-app H2C wake surface, issue #686).
- "How many requests were throttled today?" (visibility into the
  rate-limit path: per-app `gateway_rate_limited_total{app,plan}`).

Issue #696 closes the CloudWatch / Cloud Run gap. The data sources all
already exist — every Prometheus metric is already emitted, and
`usage_minutes` / `usage_daily` already populate from meterd
(ADR-045). The fetch layer (`pkg/appmetrics`) is the same primitive
the existing two endpoints reuse.

## Decision

### 1. Two new endpoints, strict separation from `/metrics`

The new wire surface is **separate endpoints**, not a flag on the
existing `/metrics` endpoints:

- `GET /v1/apps/{slug}/slo?window=24h` — per-app SLO panel.
- `GET /v1/account/slo?window=24h` — account-wide flat rollup.

Rationale: the field shapes overlap (latency percentiles, error rate,
cold-boot rate) but the new endpoints add `throttled_total`,
`instance_hours`, `gb_hours`. `wake_queue_p95_ms` is retained as a
zero-valued compatibility field until its source histogram gains an app
label; fleet data is never presented as an account or app value. Co-locating them on
`/metrics` would force `?range=` and `?window=` to live in the same
query-param namespace, and the existing 7-range closed vocabulary
(`5m|15m|1h|6h|24h|7d|15d`) is a SUPERSET of the SLO 3-window closed
vocabulary (`1h|24h|7d`). Separate endpoints make the SLO vocabulary
a strict subset and the API surface self-documenting.

### 2. Closed-set windowed vocabulary

`window` is a closed set `{1h, 24h, 7d}`. Anything else fires 400
`CodeValidation`. Default is `24h` (the canonical SLO lookback). The
vocabulary is a strict subset of `pkg/appmetrics.Ranges()` (the
/metrics 7-range set); the helper `pkg/api.SLORanges()` /
`pkg/api.IsValidSLORange()` mirrors the existing pattern.

### 3. No new Prometheus metrics, no new SQL aggregates

Every field on the SLO surface is already emitted:

- `gateway_request_duration_seconds_bucket{app,class="2xx"}` — latency.
- `gateway_requests_total{app}` — request count / error rate.
- `gateway_cold_boot_total{app}` — cold-boot rate.
- `gateway_wake_queue_wait_seconds_bucket` (unlabeled, fleet) — wake queue.
- `gateway_rate_limited_total{app, plan}` — throttled count.

`instance_hours` and `gb_hours` come from `usage_minutes` (existing
SQL surface, populated by meterd's `SampleAndRoll` loop). The pgstore
helper `UsageSLOForApp` / `UsageSLOForAccount` is the only new
SQL primitive — a single `SELECT count(*), sum(mb_seconds)` against
`usage_minutes` JOINed to `apps`.

Why no new metrics: the platform's cardinality budget is bounded by
the per-app fan-out invariant (spec §6.2). Adding `app` labels to the
wake-queue histogram would blow the histogram cardinality budget
(every app × every bucket). Deferring per-app queue p95 to a future
ADR is the right call.

### 4. Source contract: `"degraded: <reason>"`

The `source` field follows the existing `pkg/appmetrics.SourceDegradedPrefix`
contract. Two distinguishable failure modes:

- Prometheus unreachable → all numeric fields zeroed, `source =
  "degraded: prometheus not configured"`.
- Postgres `usage_minutes` rollup failed but Prometheus succeeded → only
  `instance_hours` / `gb_hours` zeroed, `source = "degraded: postgres
  unavailable"` (the latency/error/cold-boot numbers stay non-zero).

This pattern matches the existing `/metrics` empty-state UX so the
dashboard has one branch across both endpoints.

### 5. Auth chain — intentional asymmetry

- `GET /v1/apps/{slug}/slo` — `authLimited + requireScope(ScopesReadSurface)`,
  **no MFA**. Mirrors `/v1/apps/{slug}/metrics`. Primary caller is an
  API key.
- `GET /v1/account/slo` — `authLimited + requireMFA + requireScope(ScopesUsageReadSurface)`,
  MFA-gated. Mirrors `/v1/usage/summary`. The flat account rollup
  includes billing-derivable fields (`instance_hours`, `gb_hours`), so
  the `usage:read` precedent is the right fit.

The asymmetry is intentional and documented in the route registration
comment block AND this ADR.

### 6. Tier gating — open to all plans

Not gated. Free/Hobby/Pro/Scale all see their own SLO data. The data
is per-app and per-account; cardinality is bounded by the per-app
fan-out invariant. The Free tier zero-fields-when-zero-billable
convention applies (`instance_hours`, `gb_hours` = 0 for Free, since
Free has no billable units — the latency/error/cold-boot SLO
nonetheless renders).

### 7. Reuse, not refactor

The PromQL pipeline reuses the same `pkg/appmetrics.SafeFloat` /
`SafePercent` / `SafeRoundNonNeg` helpers from `pkg/appmetrics`, and
the same `histogram_quantile(...)*1000` pattern. The `'degraded:'`
sanitiser pattern is the same `strings.ReplaceAll(..., "\r", "")` /
`strings.ReplaceAll(..., "\n", "")` inline at the log call site that
the `pkg/appmetrics` package doc references (CodeQL alert #117).

The SDK parity gate (`cmd/sdk-coverage`) automatically requires the
Go SDK hand-mirror of the new DTOs (`AppSLOResponse`,
`AccountSLOResponse`, `SLODuration`) and the route → method mapping
(`GetAppSLO`, `GetAccountSLO`). Node/Python SDKs auto-regenerate via
`make sdk-gen`.

The CLI subcommand `gregale slo <slug> [--window 24h]` and `gregale
account slo [--window 24h]` mirror the existing `gregale metrics`
shape exactly (single positional slug + one flag for the per-app
sibling; no slug for the account-scope one).

## Consequences

- **New public API surface**: two GET endpoints, two new DTOs, three
  new SDK Go types.
- **New CLI subcommand**: `gregale slo` and `gregale account slo`.
- **No schema changes**, no migrations.
- **No new Prometheus metrics** — no cardinality impact.
- **No new metric labels** — wake-queue remains unlabeled fleet-wide.
- The dashboard gains an SLO card (PR-B territory — the first PR here
  ships the API surface; the card can be a block-level layout that
  iterates over time).
- The SDK parity gate acquires the two new route mappings. The
  auto-gen for node/python runs through `make sdk-gen`.

### Forward-only / rollback

- **No schema changes** — nothing to roll back at the DB level.
- **Go-side** — revert the two new routes, the two new handlers, the
  two new DTOs, the two new SDK Go DTOs, the two new CLI subcommands,
  and the parity table entries.
- **OpenAPI** — revert the two new endpoint blocks and the three new
  components (`AppSLOResponse`, `AccountSLOResponse`, `SLODuration`).
- **ADR** — leave the document in place; mark it `Status: superseded`
  with a pointer to the cancellation.

## Verification

### Unit tests

- `make test` — must pass. Primary tripwires:
  - `pkg/api/dto_test.go::IsValidSLORange` (3-window closed vocabulary).
  - `cmd/apid/handlers_slo_test.go` (3 windows × 4 plans × 2 prom
    states + IDOR probe).
  - `cmd/sdk-coverage` (existing gate upgraded with the two new routes).
  - `pkg/appmetrics` existing tests (no regression).
- `make lint` — gofmt + goconst + errorlint (per CLAUDE.md Conventions).

### Migration tests

- `make migration-test` — schema-unchanged, so this is a no-op
  tripwire. Must pass.

### End-to-end

- `make e2e-build && make e2e` — `cmd/e2e/slo_e2e_test.go` runs the
  CLI subcommand probe + IDOR probe + Free zero-billable-fields
  assertion.
- Dashboard smoke test on staging: log into an account with ≥1 app,
  navigate to `/dashboard/apps/{slug}`, confirm the SLO card renders
  with the staging window.

### Operator workflow

1. `curl -H "Authorization: Bearer $TOKEN" https://api.gregale.dev/v1/apps/my-app/slo?window=24h`
   — returns 200 with the wire shape, `source: "prometheus"`.
2. Same with `?window=invalid` — returns 400 with the 3-window closed
   set listed.
3. Kill the Prometheus client and curl again — returns 200 with
   `source: "degraded: prometheus not configured"` and zeroed fields.
4. `gregale slo my-app --window 7d` — exits 0 with the labelled block.
5. `gregale account slo --window 7d` — exits 0 with the account-wide
   rollup.
6. Dashboard: navigate to `/dashboard/apps/my-app` — the SLO card
   renders beneath the existing metrics panel.

## Dashboard render (dashboard follow-up PR)

The follow-up PR closes the remaining acceptance item from
issue #696 — the dashboard card. The card renders on the
per-app page (`/dashboard/apps/{slug}`) and the per-account
page (`/dashboard/account`) with the same nine fields and
the same "degraded:" contract the API surface ships.

### Window selector

- The 1h / 24h / 7d selector is rendered as a `<nav>` beside
  the card title. Tabs are static `<a>` links that round-trip
  the `?window=` query-string. The page reloads on tab click
  (no JS); the existing dashboard's pagination precedent uses
  the same shape.
- The handler reads `?window=` via `resolveSLOWindow` (cmd/apid
  /handlers_dashboard_slo.go). Invalid values fall back to the
  default (`24h`) rather than 400 — a typo in the URL doesn't
  500 the dashboard render.

### Sparkline source

- The time-bucketed series comes from Prometheus's
  `query_range` endpoint via `pkg/promql.Client.QueryRange`
  (added in this follow-up PR). `pkg/appmetrics.FetchRange`
  issues one query per metric (latency p50 / p95 / p99,
  error rate, cold-boot rate) and projects the result onto
  `pkg/appmetrics.SparklinePoint`.
- Bucket count: 60 for 1h (1m step), 96 for 24h (15m step),
  168 for 7d (1h step). The 168-bucket 7d is the binding
  budget against Prometheus's 15d retention.
- The PromQL strings are identical to the scalar `fetchAppSLO`
  / `fetchAccountSLO` (cmd/apid/handlers_slo.go). The headline
  number and the sparkline end-of-window value are computed
  over the same data — the stickier dashboard bug is
  "p95 = 87ms" but a sparkline that ends at 142ms because the
  two queries used different range vectors.

### Inline SVG (no JS dependency)

- The renderer is `pkg/dashboard/views/render.go`'s
  `RenderLatencySparkline` / `RenderErrorRateSparkline` /
  `RenderColdBootRateSparkline`. Pure Go; no JS bundler, no
  htmx hook. The template embeds the pre-rendered HTML.
- The latency triple is a 3-line chart (p50/p95/p99 in three
  shades of the accent colour). The error rate / cold-boot
  rate rows are filled-area charts over the same x-axis.
- Accessibility: every SVG emits `role="img"` and an
  `aria-label` describing the trend ("rising" / "falling" /
  "flat" / "no data"). The trend is computed off the p95
  sub-series for the latency chart and off the only series
  for the area charts.

### Cardinality budget

- 3 PromQL query_range calls per dashboard page render
  (one per metric per page). At the dashboard render rate
  (login + manual refresh) this is 60-90 calls/min on a
  one-box — well within the 15d Prometheus retention.
- The per-app row badge on the apps list adds 1 query_range
  per app, batched at 25 apps max
  (`dashboardSLOAppsListBadgeCap`). Beyond the cap the badge
  silently degrades to "—" — the per-app SLO panel is always
  reachable via the row's link.
- **No new metric labels, no new metrics, no new SQL aggregates
  on the read path.** Same data the API surface shipped.

### Package-isolation rule

- `pkg/dashboard/views` is the new typed view package
  (mirrors the existing `DashboardDetailData` package-isolation
  pattern). It holds the dashboard-facing mirror of
  `api.AppSLOResponse` / `api.AccountSLOResponse` so
  `pkg/dashboard` stays free of `pkg/api` imports.
- The handler (cmd/apid/handlers_dashboard_slo.go) projects
  the wire shape into the view shape. The template is a pure
  renderer.

### Auth chain — same as the API surface

- The dashboard card inherits the existing auth on
  `renderAppDetail` / `renderAccount` — both routes serve the
  dashboard cookie (CSRF + session). The wire shape behind
  the card is the same `/v1/apps/{slug}/slo` /
  `/v1/account/slo` payload the public API serves.
- **No new auth path needed.** The per-app / per-account
  asymmetry documented in §"Auth chain" carries over
  automatically.

### Status

- ADR-082 status remains `accepted`. The dashboard render
  decisions are appended here rather than in a new ADR
  because they are scoped to the same customer-facing SLO
  surface.

## Open / deferred items

1. **Per-window comparison (today vs. last_week)** — deferred. The
   3-window closed vocabulary is the answer to "what is my SLO?"
   first; "am I regressing?" is a separate dashboard feature.
2. **Per-region SLO segmentation** — deferred. The platform is
   single-region today; revisit when the multi-host control plane
   (§14 Tier A) lands.
3. **Per-app wake queue p95** — the queue histogram is unlabeled
   today (fleet-wide). Per-app queue p95 requires adding an `app`
   label to `gateway_wake_queue_wait_seconds`. That is a separate
   metric change and warrants its own ADR if the customer demand
   surfaces. Today's SLO exposes the fleet-wide number with the
   same "fleet-wide" labelling the existing `/metrics` endpoints
   use.
4. **Free-plan instance_hours / gb_hours = 0** — by design. Free has
   no billable units, so the SLO field is zeroed for shape parity.
   The Free customer sees the latency/error/cold-boot SLO; the
   billing fields say "N/A" in the UI.
5. **Dashboard SLO card** — shipped in the dashboard follow-up PR
   (the same branch series that landed this ADR). See §"Dashboard
   render" below for the rendering decisions. The card renders all
   9 fields from the wire shape plus a 3-line latency sparkline,
   filled-area error-rate / cold-boot-rate sparklines, and a
   1h / 24h / 7d window selector that round-trips the URL
   query-string.
6. **Threshold breach alerts** — a future feature. The SLO surface
   is the read substrate; the alert evaluator (issue #396 / ADR-045
   PR 4) is the consumer that turns breaches into pages.
