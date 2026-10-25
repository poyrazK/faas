# ADR-20261024154324: Route subsystems at public scale

- **Status:** accepted
- **Date:** 2026-10-24
- **Related:** ADR-093 (per-route metrics), ADR-1198 (route insights),
  ADR-498/499 (production route monitoring), ADR-845 (monitor rollback),
  ADR-449 (automatic route checks)
- **Amends:** ADR-093 D2 (non-evicting series), ADR-1198 (candidate cap)

## Context

A public-release review of the routes product found three limits that hold
for today's fleet but break as it grows, and background workers no operator
alert covers:

1. **Per-route Prometheus series.** ADR-093 sized the series for about 100
   opted-in apps, with the feature off by default. Every Hobby, Pro and Scale
   app is now opted in by default and the ansible default turns the feature
   on for every cluster. Each app can hold 50 route labels, each pre-creating
   four histogram classes of 14 samples, and the gateway never deleted a
   series until it restarted, so the export grew with every app a gateway had
   served. No alert watched the result.
2. **Route insights stopped at 500 apps.** The evaluator read the apps that
   ran in the last day ordered by app ID with `LIMIT 500`. Free apps took
   slots and were then skipped. Past 500 active apps, apps whose IDs sort late
   never got insights, and nothing reported it. Insights of apps that stopped
   running were never resolved, because only candidates were resolved.
3. **The production route monitor evaluated about 40 monitors a minute.** It
   read 20 due monitors every 30 s and evaluated them one by one, while each
   monitor is due every minute. Past 40 enabled monitors per replica, checks
   and the opt-in automatic rollback, which acts only within 30 minutes of an
   incident, fell behind with no measure of the backlog.
4. **No operator signals.** The insights evaluator, the route monitor and the
   automatic route checks logged warnings only.

## Decision

**Per-gateway series budget.** Each `gatewayd-internal` keeps a
`routeSeriesBudget`:

- At most `RouteMetricsMaxRoutesPerGateway` (5,000) (app, route) pairs carry
  live `gateway_*_by_route` series; `FAAS_GATEWAY_ROUTE_METRICS_MAX_ROUTES`
  overrides it. A pair beyond the budget is not exported and counts in
  `gateway_route_metrics_rejected_total`. The request still counts in the
  per-app series and request telemetry, which route insights, the awake
  report, route health and the route monitor read.
- An app's per-route series are deleted after `RouteMetricsSeriesIdleTTL`
  (1 h) without a request on that gateway, checked every
  `RouteMetricsSweepInterval`. Deletion uses exact label values recorded at
  emission; a partial-match delete would hold the vector's write lock for a
  scan of every series and stall the hot path. The app's next request
  re-creates the series; counters restart, which `rate()` and `increase()`
  absorb.
- The histogram classes are pre-instantiated when the budget first admits a
  route, so a re-created route is pre-instantiated again.
- The per-app `routeLabelSet` stays non-evicting (ADR-093 D2), so labels stay
  stable across eviction and request telemetry is unchanged. Only the
  Prometheus series are evicted.

apid's route listing (`GET /v1/apps/{slug}/routes`) read the current series
with an instant query. It now reads
`last_over_time(...[RouteMetricsObservedWindow])` (7 days), so an evicted
app's routes stay listed. The throttle advisor and the recording rules already
use range queries.

**Fair insight scheduling.** `route_insight_evaluations` records when each app
was last evaluated. A run reads candidates in batches of
`RouteInsightBatchApps`: apps that ran in the window, on plans with request
telemetry, not evaluated within half an interval, least recently evaluated
first. Each app is claimed with a conditional upsert before evaluation, so
replicas share the work and never evaluate one app twice in a run. The run
continues until no candidate is due. Stale insights of every app resolve in
one bounded batch (`RouteInsightResolveBatch`) per run, including apps that
stopped running or left a telemetry plan.

**Concurrent monitor drain.** `routejobs.MonitorDrain` keeps reading due
monitors until none it has not already taken is due or
`RouteMonitorDrainBudget` (a minute) passes, and evaluates
`RouteMonitorWorkerConcurrency` (4) at a time. Replicas still share the queue
through `FOR UPDATE SKIP LOCKED`. The automatic route checks already drain 120
checks a minute, which deployments do not approach, so only their metrics
change.

**Operator signals.** New metrics: `gateway_route_metrics_routes`,
`gateway_route_metrics_budget_routes`, `gateway_route_metrics_rejected_total`,
`gateway_route_metrics_evictions_total`;
`apid_route_insights_runs_total{outcome}`,
`apid_route_insights_apps_total{outcome}`,
`apid_route_insights_resolved_total`,
`apid_route_insights_last_success_timestamp_seconds`;
`apid_route_monitor_evaluations_total{outcome}`,
`apid_route_monitor_lag_seconds`,
`apid_route_monitor_last_drain_timestamp_seconds`;
`apid_route_checks_total{outcome}`,
`apid_route_worker_queue_unavailable_total{worker}`. Five warn alerts use them
(`GatewayRouteMetricsBudgetExhausted`, `FaasRouteInsightsStalled`,
`FaasRouteInsightsFailing`, `FaasRouteMonitorLagging`,
`FaasRouteWorkersFailing`), with runbooks `GatewayRouteMetricsBudget.md` and
`FaasRouteWorkers.md`.

## Consequences

- A gateway's per-route export is bounded by the budget, worst case about
  280,000 histogram series plus counters, and in practice tracks the apps
  active in the last hour or two. Prometheus head memory follows.
- Under budget pressure some routes are missing from that gateway's
  per-route series, and an operator is told. Nothing customer-facing breaks.
- An app whose traffic returns after an idle hour loses the first request's
  increment in `increase()` over a window that starts before the series was
  re-created, as for any newly created series.
- Every app on a telemetry plan gets insights however many apps run, and
  insights of apps no longer evaluated resolve within a day.
- Route monitor throughput scales with concurrency and replicas instead of
  with the batch size, and lag is visible.

## Not decided here

A fleet-wide (rather than per-gateway) series budget, dropping the
pre-instantiated histogram classes, and per-plan budgets.
