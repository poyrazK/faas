# GatewayRouteMetricsBudgetExhausted

Severity: warn. ADR-093, ADR-20261024154324.

Every Hobby, Pro and Scale app exports per-route series
(`gateway_requests_by_route_total`, `gateway_request_duration_by_route_seconds`,
`gateway_request_failures_by_route_total`). Each `gatewayd-internal` bounds how
many (app, route) pairs it exports at once: `gateway_route_metrics_routes` is
the live count, `gateway_route_metrics_budget_routes` the bound
(`api.RouteMetricsMaxRoutesPerGateway`, overridden by
`FAAS_GATEWAY_ROUTE_METRICS_MAX_ROUTES`). An app's series are deleted after an
hour without a request on that gateway, which frees its pairs. When the budget
is full, new pairs are not exported and `gateway_route_metrics_rejected_total`
counts them. The alert fires when a node has declined pairs for 15 minutes.

## Symptom

Requests are unaffected: the per-app series and request telemetry still count
every request. What degrades is the per-route Prometheus view on that node:
`gregale app <slug> routes`, automatic OpenAPI documents, contract previews and
throttle suggestions miss the routes that were declined. Route insights, the
awake report, route health and the route monitor read request telemetry from
Postgres and are not affected.

## Check

1. `gateway_route_metrics_routes / gateway_route_metrics_budget_routes` by
   `instance`: one node near 1, or all of them?
2. `rate(gateway_route_metrics_evictions_total[1h])`: zero evictions while the
   live count stays at the budget means the active app set really outgrew it,
   not a stuck sweeper.
3. `prometheus_tsdb_head_series` on the Prometheus host, and its memory, to
   see whether there is room for more series.
4. `topk(10, count by (app) (gateway_request_duration_by_route_seconds_count))`
   to find apps holding the most pairs; a wildcard-shaped app also raises
   `GatewayWildcardRoute`.

## Recover

- With Prometheus headroom, raise the bound on the node:
  `FAAS_GATEWAY_ROUTE_METRICS_MAX_ROUTES=<pairs>` in the
  `faas-gatewayd-internal` environment, then restart the unit. Each pair costs
  up to four histogram series of 14 samples plus one counter per status code.
- Without headroom, leave the bound: the node keeps exporting its current
  pairs and idle apps free theirs within the hour. Adding a gateway node
  spreads the active apps.
- To stop per-route series on a node entirely, use the ADR-093 kill switch
  (`gatewayd_route_metrics_enabled=false`, or `FAAS_GATEWAY_ROUTE_METRICS=false`).
