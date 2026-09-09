# ADR-127 · Production debugger — per-request telemetry + customer OTel ingest + mirror replay

- **Status:** proposed
- **Date:** 2026-08-23
- **Issue / PR:** TBD (PR-A of a 3-PR cluster; PR-A ships the data plane, PR-B the cron + alerting, PR-C the LLM synthesis layer)
- **Decision:** Persist one row per gateway-served request in a new `request_telemetry` table keyed on `trace_id`; extend `gateway_request_duration_seconds` with a `deployment` label bounded by a `deploymentLabelSet` cardinality cap (overflow → `__other__`); expose `POST /v1/otel/v1/traces` so customer apps can ingest their own OTel spans linked by `trace_id` to the persisted row; add `POST /v1/apps/{slug}/debug/requests/{req_id}/replay` that re-issues the captured request through an ADR-125 mirror rule. PR-A ships the data plane; PR-B adds cron-based regression detection; PR-C adds LLM prose synthesis on top.

## Context

Today's Gregale answers "is my app slow?" via `gateway_request_duration_seconds{app, class}` (`pkg/gateway/metrics.go:811-817`) but cannot answer "**did v81 make it slow?**". Three gaps block the question from being derivable:

1. **No deployment dimension on the latency histogram.** Labels are `{app, class}` only; `Target.DeploymentID` (`pkg/gateway/pgbackend.go:529, 702, 710, 926`) flows through the request path for routing/picker but never becomes a metric label.
2. **No per-request persisted row.** `usage_minutes` (`pkg/meter/sampler.go:441`) stores `mb_seconds + requests + cpu_usec + tx_bytes + cold_boot_count + tail_seconds`, keyed by `(instance_id, minute)`, with no latency/status by deployment. The only comparator (`mirror_invocation_results`, `migrations/00386_mirror_invocation_results.sql`) is gated behind an explicit mirror rule and only fires when the customer opted into mirroring — most requests are not mirrored.
3. **No inside-VM breakdown.** Gregale hands the guest a W3C `traceparent` (`guest/init/app.go:138-153`), but the customer's PostgreSQL queries, downstream HTTP calls, and code-level timings are opaque unless the customer emits OTel spans themselves. TraceRing (`pkg/gateway/trace_ring.go:43-47`) is 24h in-memory and single-box.

Operators investigating a Hobby-tier latency regression today must correlate disjoint signals by hand: `gateway_request_duration_seconds` (no deployment), `usage_minutes` (no latency), `mirror_invocation_results` (only when mirrored), `app_errors` (only on 4xx/5xx), `events` (only for wakes). The example insight — *"POST /checkout became 38% slower after deployment v81; PostgreSQL queries 82ms → 191ms; 31% of requests affected; started 18:42 UTC"* — is unanswerable end-to-end.

Cloud Run ships `request_log` + `traces` for the same diagnosis. The data plane is the load-bearing piece; an LLM on top is secondary.

## Decision

### 1. New `request_telemetry` table

Migration `00427_request_telemetry.sql`. Schema:

```sql
CREATE TABLE public.request_telemetry (
    id              UUID DEFAULT gen_random_uuid(),
    account_id      UUID NOT NULL,
    app_id          UUID NOT NULL,
    deployment_id   UUID NOT NULL,
    route           TEXT NOT NULL,
    method          TEXT NOT NULL,
    status          INT  NOT NULL CHECK (status BETWEEN 100 AND 599),
    latency_ms      INT  NOT NULL CHECK (latency_ms >= 0), -- bounded bucket upper bound for collapsed rows
    cold_boot       BOOLEAN NOT NULL DEFAULT false,
    trace_id        TEXT,                              -- W3C trace-id hex (32 chars)
    spans_summary   JSONB,                             -- ADR-127 §3 customer OTel ingest
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, received_at)
) PARTITION BY RANGE (received_at);

CREATE INDEX request_telemetry_app_received_idx
    ON public.request_telemetry (app_id, received_at DESC);
CREATE INDEX request_telemetry_app_dep_received_idx
    ON public.request_telemetry (app_id, deployment_id, received_at DESC);
CREATE INDEX request_telemetry_trace_idx
    ON public.request_telemetry (trace_id) WHERE trace_id IS NOT NULL;
```

**Monthly partitioning** keeps query plans bounded and makes the retention sweep a partition-level DROP rather than a per-row DELETE. Indexes mirror the `mirror_invocation_results_rule_time_idx` precedent (`migrations/00386_mirror_invocation_results.sql:119-120`).

Replay-safe posture (`IF NOT EXISTS` / `IF EXISTS` everywhere, `TestNewMigrationsAreReplaySafe`-compatible) per `migrations/00386_mirror_invocation_results.sql:90-93` and ADR-041.

### 2. Recorder + publisher (gatewayd-internal)

New middleware `pkg/gateway/request_telemetry.go` mirroring `cmd/gatewayd-internal/app_errors_recorder.go` shape:

- `requestTelemetryRecorder` holds an in-process ring buffer (`ringBufferSize=4096`). The publisher collapses by `(app_id, deployment_id, route, method, status, dimensions, minute, bounded latency bucket)` into counted representatives so percentile queries retain distribution signal without persisting one row per request.
- `Middleware(next)` returns `http.Handler`. Reads `r.Context()` keys already populated by gateway middleware (`gregale.account_id`, `gregale.app_id`, `gregale.deployment_id`, `gregale.route_template`) — same keys `app_errors_recorder.go:475-478` uses.
- **Hot path wired at `Handler.observe`** (`pkg/gateway/handler.go:5456-5524`) — extend `observe` to enqueue a row before returning. Status + elapsed + app + deployment + route + cold are all already resolved at that site.

Publisher `pkg/gateway/request_telemetry_publisher.go` next to `app_errors_publisher.go:46-66`:

- Long-lived streaming gRPC to apid's `IncrementRequestTelemetry` RPC.
- `FlushInterval=5s`, `FlushBatchSize=256`, `FlushRPCTimeout=30s`, `FlushMaxConsecutiveFailures=5`.
- Same retry/posture as `app_errors_publisher.go:147-278`.

**Ownership:** `cmd/gatewayd-internal/app_errors_recorder.go:17-21` makes the rule explicit — *"The recorder never opens a Postgres connection (CLAUDE.md ownership: apid is the sole writer)"*. The same applies here: the recorder publishes via unix-socket gRPC to apid. No precedent exists for gatewayd-internal opening its own Postgres pool, and PR-A does not create one.

### 3. apid receiver (sqlc + gRPC streaming)

New `cmd/apid/grpc_server_request_telemetry.go` mirroring `cmd/apid/grpc_server_apperrors.go:92-252`:

- `IncrementRequestTelemetry(stream)` streaming RPC.
- Per-record handler invokes the sqlc-generated `s.store.InsertRequestTelemetry(ctx, params)`.
- Per-account rate-cap check via `limits.DebugTelemetryRequestsPerMinute` token-bucket; overflow returns outcome `{code: RATE_LIMITED, retry_after}` rather than dropping silently.
- Kill-switch via `FAAS_REQUEST_TELEMETRY_ENABLED` env var.

New queries in `pkg/state/queries.sql` (next to the `IncrementAppError` block at `:906-947`):

```sql
-- name: InsertRequestTelemetry :exec
INSERT INTO public.request_telemetry (
    account_id, app_id, deployment_id, route, method,
    status, latency_ms, cold_boot, trace_id, received_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListRequestTelemetryByApp :many
-- (per-app, time-windowed, paginated)

-- name: RequestTelemetryByDeployment :many
-- (per-deployment drilldown)

-- name: RequestTelemetryRegression :many
-- (CTE: baseline = p95 per route from prior deployment;
--  return rows where latency_ms > 1.20 * baseline, joined to current deployment)
```

Regenerate via `make sqlc generate`. Add typed methods to `Store` interface at `pkg/state/store.go` near `IncrementAppError`.

### 4. Extend `gateway_request_duration_seconds` with `deployment` label

`pkg/gateway/metrics.go:811-817`:

```go
dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
    Name:    "gateway_request_duration_seconds",
    Help:    "Per-request gateway duration, sliced by deployment.",
    Buckets: prometheus.DefBuckets,
}, []string{"app", "deployment", "class"})
```

**Cardinality cap** via new `pkg/gateway/deployment_label_set.go` mirroring `pkg/wire/account_label_set.go` (`metrics.go:256, 312, 414, 418, 611`):

- Per-app cap = `DebugTelemetryDeploymentsPerApp` (new limit; Hobby=10, Pro=50, Scale=200).
- Overflow → `__other__` (closed enum discipline).
- Asserted by extending `pkg/gateway/metrics_cardinality_test.go`.

`PreInstantiateApp` (`pkg/gateway/metrics.go:1383`) extended to pre-instantiate closed `(app, deployment, class)` triplets at app-create time so panels surface from request 1, mirroring the existing `accountLabelSet` discipline.

### 5. Customer OTel ingest

New `POST /v1/otel/v1/traces` endpoint following OTel HTTP/JSON protobuf:

- Auth: per-account `Authorization: Bearer <token>` (validated against apid's `api_keys` table via loopback RPC).
- Span processor: when a span arrives with a `trace_id` matching an existing `request_telemetry.trace_id`, persist span-summary (top-N slowest children, DB-statement attributes if present) into the row's `spans_summary` jsonb column.
- Limit: customer span ingest capped at `DebugTelemetrySpansPerTrace` (Hobby=50, Pro=200, Scale=1000); overflow truncated.
- Reuses `TraceRingExporter` plumbing at `pkg/gateway/trace_setup.go:131` as the seam.

This is what makes the example "PostgreSQL queries 82ms → 191ms" breakdown derivable: the customer emits DB spans tagged with the traceparent Gregale set in `TRACEPARENT` env, Gregale merges them into the persisted request row.

> Writer detail: `docs/adr/adr-127-pr-d.md` — describes the
> gatewayd-public → apid write path, auth RPC, rate-limit regime,
> truncation policy, and coalesce-then-flush loop. PR-D implements
> this section; PR-C (synthesis) reads it.

### 6. Replay (mirror-rule reuse)

New `POST /v1/apps/{slug}/debug/requests/{req_id}/replay`:

- Captures (status, route, method, deployment_id, trace_id) from the persisted `request_telemetry` row.
- Re-issues via the existing ADR-125 mirror rule machinery: `schedd` invokes the customer's request against the chosen target deployment, returns `{source: latency_ms, mirror: latency_ms}`.
- No new capture-store primitive needed (PR-A reject alternative §1).
- The mirror result lands in the existing `mirror_invocation_results` table (`migrations/00386_mirror_invocation_results.sql:41-117`), already 7-day retained.

### 7. Customer-visible surfaces

| Surface | Verb | Notes |
|---|---|---|
| apid | `GET /v1/apps/{slug}/debug/requests` | ListRequestTelemetryByApp — time-windowed, paginated |
| apid | `GET /v1/apps/{slug}/debug/requests/{req_id}` | Single row + linked trace spans |
| apid | `GET /v1/apps/{slug}/debug/regressions` | RequestTelemetryRegression — active regressions since last deploy |
| apid | `POST /v1/apps/{slug}/debug/compare` | Per-route p50/p95/p99 split between two deployments |
| apid | `POST /v1/apps/{slug}/debug/requests/{req_id}/replay` | §6 |
| apid | `POST /v1/otel/v1/traces` | §5 |
| CLI | `gregale debug requests {list,get,replay}` | `cmd/gregale/commands_debug.go` |
| CLI | `gregale debug regressions <slug>` | |
| CLI | `gregale debug compare <slug> --source v80 --mirror v81` | |
| Dashboard | `/dashboard/apps/{slug}/debug` | `pkg/dashboard/templates/app_debug.html` |
| Prometheus | `FaasDebugTelemetryIngestStalled` page alert | `deploy/ansible/roles/prometheus/files/faas.rules.yml` |

## Why now

Issue #517 closed the wake timeline. Issue #477 closed consumer_keys. The mirror comparator (ADR-125, PR #1019) shipped the dual-fire primitive but no customer-facing surface. The "did v81 make it slow?" question is the most-asked operator question on Hobby / Pro plans; the answer is unanswerable today. PR-A lands the data plane so PR-B (cron + alerting) and PR-C (LLM synthesis) can compose on top.

## Consequences

- **Positive**: the example insight is fully derivable end-to-end. Operators stop correlating disjoint signals by hand. Hobby-tier SLA investigations become one query. Cloud-Run parity for request traces + regressions.
- **Positive**: supersedes the `gateway_request_duration_seconds{app}` blind spot for any future per-deployment histogram (`gateway_wake_latency_seconds`, queue depth, etc.) — the `deploymentLabelSet` pattern is reusable.
- **Negative**: per-request write amplification. At 1k RPS sustained, ~86M rows/day before partitioning; monthly partitions keep index size bounded, but Postgres IOPS grows. Mitigated by the per-account rate cap (`DebugTelemetryRequestsPerMinute`).
- **Negative**: latency percentiles are quantized to bounded bucket representatives (10ms through 1s, widening for slow outliers). This introduces a conservative error bounded by the selected bucket width, but avoids the materially worse per-minute-maximum bias of the original collapse.
- **Negative**: customer OTel ingest opens a new auth surface (`/v1/otel/v1/traces`). Mitigated by `api_keys` validation via loopback RPC; per-account rate limit; span count cap.
- **Compatibility**: additive — no existing endpoint, table, or wire field changes. The recorder hot path is one extra call at `Handler.observe`; the histogram label set widens (Prometheus rollups for `{app, class}` continue to work as `deployment="*"` aggregates).
- **Migration**: one new table (`00427_request_telemetry.sql`), replay-safe. Slot 387 confirmed unowned (only PR #1024 and PR #1049 hold reservations at that slot; reservations carve-out per `scripts/ci/check_migration_slots.sh`).
- **Tests**: recorder unit tests (`pkg/gateway/request_telemetry_test.go`); apid handler tests (`cmd/apid/handlers_debug_telemetry_test.go`) using `state.MemStore` per `handlers_invocations_test.go:42-72`; cardinality invariant test (`metrics_cardinality_test.go` extension); sqlc partition pruning test.

## Rejected alternatives

1. **Capture raw request bodies (headers + body bytea).** Rejected. Privacy cost, body-size cap (existing `MaxSourceBytesPerInvocation`), PII risk. Mirror-based replay (§6) gives the customer's "does v80 still answer the same way?" primitive without the privacy surface. The captured row's `route + method + status + latency_ms + trace_id` is sufficient to derive the example insight; bodies are recoverable via the customer's own logs.
2. **Sample-only data plane (head-ratio sampler).** Rejected. Regression detection would silently miss the worst cases (the ones the customer notices). A 1k-RPS app at 10% sample = 100 rows/sec; that's enough to detect a regression but not to attribute it. Explicit reject.
3. **Per-app `TraceRing` on disk.** Rejected. Replaced by sqlc persistence with monthly partitions. TraceRing's 24h in-memory cap (`pkg/gateway/trace_ring.go:43-47`) is for *live* debugging; the regression-detection data plane needs weeks.
4. **Add LLM synthesis in PR-A.** Rejected for PR-A scope. PR-C composes on the data plane.
5. **Use `mirror_invocation_results` as the only storage.** Rejected. Most requests aren't mirrored — `mirror_invocation_results` is only populated for explicit mirror rules (ADR-125). The 99% of requests that aren't mirrored are exactly the ones the operator needs to debug.
6. **Derive `request_telemetry` from the OTel span stream.** Rejected. `pkg/wire/otelinit/sampler.go:218-221` notes gateway request spans fall through to head-ratio (sampled subset), and gateway spans don't stamp `deployment_id`. The data plane needs every row, sampled or not — derive from `Handler.observe` where all fields are already resolved.

## Neighbors

- **ADR-064** (wake timeline vocabulary — additive payload pattern; PR-A's `request_telemetry` follows the same replay-safe migration posture).
- **ADR-070** (gateway split — gatewayd-public / gatewayd-internal ownership; PR-A's recorder lives in gatewayd-internal via `pkg/gateway/`, ships to apid via unix-socket gRPC).
- **ADR-120** (Domain Doctor — same periodic-probe → persisted-observation shape; PR-A's debugger is the customer-facing analog).
- **ADR-123** (wake-boot telemetry — same additive-payload + per-account-limit pattern; PR-A extends `pgstore` with sqlc queries).
- **ADR-125** (traffic mirroring — direct dependency; PR-A's replay uses the mirror rule machinery, and the mirror comparator's `latency_ms` columns are the basis for compare).
- **ADR-122** (canonical metrics-listener shape — PR-A's `deploymentLabelSet` follows the same closed-enum discipline as `accountLabelSet`).
- **ADR-041** (migration slot reservation — PR-A picks slot 387 per the carve-out).

## Critical files

- `migrations/00427_request_telemetry.sql` — NEW
- `pkg/api/limits.go` — extend with `DebugTelemetryEnabled`, `DebugTelemetryRetentionDays`, `DebugTelemetryRequestsPerMinute`, `DebugTelemetryDeploymentsPerApp`, `DebugTelemetrySpansPerTrace`
- `pkg/api/errors.go` — extend with `CodeDebugQuotaReached`, `CodeDebugFeatureGated`, `CodeDebugReplayUnsupported`
- `pkg/api/debug_telemetry.go` — NEW, request/response DTOs
- `pkg/state/queries.sql` — `InsertRequestTelemetry`, `ListRequestTelemetryByApp`, `RequestTelemetryByDeployment`, `RequestTelemetryRegression`
- `pkg/state/store.go` — `Store` interface additions
- `pkg/state/pgstore.go` — sqlc receivers
- `pkg/gateway/metrics.go:811-817` — extend histogram label set; `metrics.go:1383` `PreInstantiateApp` extension
- `pkg/gateway/deployment_label_set.go` — NEW, cardinality cap
- `pkg/gateway/request_telemetry.go` — NEW, middleware
- `pkg/gateway/request_telemetry_publisher.go` — NEW, gRPC streaming client
- `pkg/gateway/handler.go:5456` — `Handler.observe` extension
- `pkg/gateway/otel_ingest.go` — NEW, OTLP/HTTP receiver
- `pkg/gateway/trace_setup.go:131` — extend `TraceRingExporter`
- `cmd/apid/grpc_server_request_telemetry.go` — NEW, receiver
- `cmd/apid/handlers_debug_telemetry.go` — NEW, 5 endpoints
- `cmd/apid/handlers_otel.go` — NEW, OTLP receiver entry
- `cmd/gregale/commands_debug.go` — NEW, CLI
- `pkg/dashboard/templates/app_debug.html` — NEW
- `pkg/dashboard/dashboard.go` — `DebugTelemetryView` struct
- `pkg/dashboard/views/render.go` — regression sparkline
- `pkg/meter/retention.go` — `RetentionOnceRequestTelemetry`
- `cmd/meterd/main.go:926-928` — wire sweep
- `api/openapi.yaml` — paths + schemas
- `deploy/ansible/roles/prometheus/files/faas.rules.yml` — 2 alerts

## PR-A follow-ons (NOT this ADR)

- **PR-B**: cron-based regression detector (cron-driven sweep every N minutes; `CronLimitPerApp`-style cap; alerts `FaasDebugRegressionDetected`).
- **PR-C**: LLM synthesis layer that takes a regression row + spans_summary and produces prose ("primary change is your DB query: 82→191ms; 31% affected; [Compare v80] [Replay] [Rollback]").
- **Per-account OTel ingest auth**: currently validated against apid's `api_keys`; may need a separate `otel_ingest_keys` table if customer-side token rotation churn becomes a problem.
- **Cross-app debugger view**: operator-only, surfaces regressions across the fleet (mirror the `gregale compute_nodes show` pattern from PR #1044).
