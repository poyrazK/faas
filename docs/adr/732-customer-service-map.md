# ADR-732 · Customer service map from unsampled service-proxy edges

- **Status:** proposed
- **Date:** 2026-10-08
- **Decision:** Expose an account-scoped, read-only service map at
  `GET /v1/service-map?range=` built from the existing unsampled
  `gateway_service_dependency_edge_calls_total` counter and
  `gateway_service_dependency_duration_seconds` histogram (ADR-288). The
  capability is `internal` and dark behind `FAAS_SERVICE_MAP_ENABLED=1`
  until the CLI, dashboard, and documentation slices land.
- **Why:** Every same-account internal call already crosses
  `gatewayd-internal`, which attributes it to a trusted caller and target app
  (ADR-169, ADR-206) and records it without sampling. Today only operator
  Grafana and the `FaasServiceDependencyErrorsHigh` alert read those series.
  Customers who split an API into services have to instrument every service
  to learn who calls whom, how often, and how reliably — information the
  platform already holds. A zero-instrumentation map is an observability
  surface no external agent can reproduce without per-service setup.
- **Consequences:**
  - New response type `api.ServiceMapResponse` with `nodes` (apps that appear
    on at least one edge) and `edges` (caller → target with calls, errors,
    error rate, and success-latency p50/p95).
  - Two PromQL round-trips per request regardless of app count: one grouped
    counter query and one grouped bucket query. Both selectors constrain
    **both** `caller_app` and `target_app` to the account's closed app-ID set
    before Prometheus evaluates them, and the handler drops any returned edge
    whose endpoints are not both owned by the account. Cross-account edges
    cannot exist today (the mesh is same-account), so this is a second
    boundary, not the primary one.
  - Latency percentiles use `outcome="success"` only, matching the 2xx-only
    convention of `/v1/apps/metrics`; fast failures would otherwise pull the
    percentiles down.
  - The range vocabulary, plan gate (`PerAppMetricsAllowed`, Hobby+), and
    degraded-source contract (`source: "degraded: <reason>"`, no partial
    rows) are shared with `/v1/apps/metrics`.
  - Edges are sorted by call volume and capped at `ServiceMapMaxEdges`
    (`pkg/api/limits.go`); `truncated: true` reports the cap.
  - Flag off answers `503 service_map_unavailable`, matching other dark
    previews.
- **Rejected alternatives:**
  - *Derive edges from sampled OTLP dependency spans in `request_telemetry`.*
    Spans are sampled and slowest-span truncated (see the API-hosting
    roadmap's release-safety section); they cannot give honest call counts or
    error rates.
  - *Add a `caller_app_id` column to `request_telemetry`.* A new write path
    and migration for data Prometheus already holds unsampled; revisit only if
    per-route edges are required.
  - *Include apps with no edges as isolated nodes.* Unbounded for large
    accounts and uninformative; the dashboard can join the app list when it
    needs them.

## Follow-ups

1. `gregale services map [--range] [--json]` CLI.
2. Dashboard panel and `docs/service-map.md`; promote to `preview`.
3. Per-edge wake counts once the service proxy records whether a call woke a
   parked target.
