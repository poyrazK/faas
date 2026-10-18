# ADR-1131 · Customer service map from unsampled service-proxy edges

- **Status:** proposed
- **Date:** 2026-10-12
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
  - A fixed number of PromQL round-trips per request regardless of app count
    (two at first, one grouped counter query and one grouped bucket query;
    the sections below add four). Every selector constrains
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

## Implementation

The handler and PromQL projection live in `pkg/apid/servicemap` (ADR-982);
`pkg/apid/apidserver` only mounts `GET /v1/service-map` and
`GET /dashboard/service-map` and implements its `Host`. The same change ships
the CLI view (`gregale metrics --services [--range] [--json]`, a flag on the
existing Observe command rather than a new noun), the dashboard page, and
`docs/service-map.md`. The capability stays `internal` behind
`FAAS_SERVICE_MAP_ENABLED` until it has been exercised on a live fleet.

## Cold calls and the wake-ahead suggestion

The service proxy additionally counts, per trusted caller/target UUID pair,
the edge calls that found the target parked and were held while it restored:
`gateway_service_dependency_edge_wakes_total{caller_app,target_app}`. It is a
new counter rather than a label on `gateway_service_dependency_edge_calls_total`,
so the existing series, alert rules and dashboards are unchanged. The map reads
it with a third bounded PromQL query (same closed-set selectors) and reports
`cold_calls` and `cold_call_pct` per edge; gateways that predate the counter
simply report zero.

Each node carries the app's ADR-977 opt-in (`wake_ahead_enabled`), and an edge
gets `suggest_wake_ahead` when its caller has not opted in, at least
`ServiceWakeAheadMinWakes` (20) calls in the window were cold, and they are at
least `ServiceWakeAheadMinColdShare` (half) of the edge's calls — ADR-977's own
evidence bar, so the map never suggests what wake-ahead would not act on. The
CLI and dashboard turn it into a one-line `gregale wake-ahead <caller> on`
hint. A failed opt-in lookup leaves that caller unmarked and unsuggested
rather than failing the map.

## Wake-ahead payoff per edge

ADR-977's outcome counter carries no app labels, so the map could suggest
wake-ahead but not show whether it then pays off. ADR-1152 since made each
wake-ahead claim remember the caller whose wake predicted it, and
gatewayd-internal counts
`gateway_service_wake_ahead_edge_total{caller_app,target_app,outcome}` (trusted
UUIDs only; the outcome-only counter is unchanged). The map reads that series
rather than adding its own. Series are bounded by ADR-977's learner bounds
(4,096 callers × 16 edges). A fourth bounded PromQL query fills
`wake_ahead_started` and `wake_ahead_used` per edge; the CLI prints
"Wake-ahead: N of M restores used" and the dashboard adds a column.

## Private TCP sessions on the map

Apps also reach each other over private TCP (ADR-576) — databases, queues and
other non-HTTP services — and those sessions never crossed the HTTP edge
counters, so a service talking to a sibling Postgres looked disconnected.
The TCP proxy now counts each session that passed identity and authorization
on its caller → target pair:
`gateway_service_tcp_edge_sessions_total{caller_app,target_app,outcome}`
(`success` or `error`) and `gateway_service_tcp_edge_wakes_total{caller_app,target_app}`
for sessions that had to restore a parked target, trusted UUIDs only. Every
refusal after authorization (no replica, wake failure, unreachable guest,
release or chaos faults) counts as an error, like a final 5xx on HTTP; the
account's plan session limit is the caller's own quota, like a 4xx, and is
not counted. The existing account-level `tcpmetrics` series are unchanged.

Two more bounded PromQL queries (same closed-set selectors; six per request
now) fill `tcp_sessions`, `tcp_errors` and `tcp_cold_sessions`. Edges are the
union of HTTP and TCP pairs, ranked by calls plus sessions, so a TCP-only
pair is an edge with zero `calls` and zero latency. Because ADR-1134 teaches
wake-ahead from private TCP sessions too, the wake-ahead suggestion counts cold
calls plus cold sessions against calls plus sessions.

## Follow-ups

1. Promote to `preview` once the map has been checked against live edges.
