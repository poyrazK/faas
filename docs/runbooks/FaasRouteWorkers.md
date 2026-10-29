# FaasRouteWorkers

Severity: warn. ADR-1198, ADR-498, ADR-449, ADR-20261024154324.

apid runs three background route workers. Every replica runs each of them;
they share the work through Postgres claims, so one healthy replica is enough.

| Worker | Cadence | What customers lose when it stops |
|---|---|---|
| Route insights evaluator (`routeinsights`) | every 6 h | new route insights, their webhooks and the "New route insights" alert |
| Production route monitor (`routejobs.MonitorDrain`) | every 30 s, each monitor due every minute | route budget incidents and opt-in automatic rollbacks |
| Automatic route checks | every 2 s | route requirement check results on new deployments |

## FaasRouteInsightsStalled

No replica has finished an insights run without error for 13 hours.

1. `apid_route_insights_runs_total` by `outcome`: runs failing, or no runs at
   all? No runs means the evaluator never started; check that apid uses the
   Postgres store.
2. `journalctl -u faas-apid | grep 'route insights'` for the failing query.
3. A run walks every app on a plan with request telemetry that ran in the last
   day. If runs succeed but take hours, `apid_route_insights_apps_total` grows
   slowly; check Postgres load from `usage_minutes` and `request_telemetry`
   reads.

## FaasRouteInsightsFailing

More than one run failed, or more than a quarter of evaluated apps failed, in
13 hours. A failed run is a failed candidate read, claim or stale resolve; a
failed app is a failed read of its telemetry or a failed insight write. The
warning names the app and the error. Insights of apps that are not evaluated
resolve after a day, so persistent failures also resolve customers' insights.

## FaasRouteMonitorLagging

`apid_route_monitor_lag_seconds` is how long the oldest due monitor had waited
when the latest drain started. A drain evaluates
`RouteMonitorWorkerConcurrency` monitors at a time until none is due or a
minute passes, so lag above five minutes means more monitors are due per
minute than the replicas evaluate.

1. `rate(apid_route_monitor_evaluations_total[5m])` by `outcome`: a high
   `skipped` rate means replicas contend for the same monitors; a high
   `failed` rate also raises `FaasRouteWorkersFailing`.
2. `count(route_monitors WHERE enabled)` in Postgres against the evaluation
   rate.
3. Add an apid replica, or raise `RouteMonitorWorkerConcurrency` in
   `pkg/api/limits.go` if Postgres has headroom.

## FaasRouteWorkersFailing

A worker could not read its queue more than twice in 15 minutes
(`apid_route_worker_queue_unavailable_total` by `worker`), more than 10% of
monitor evaluations failed, or more than half of route checks were retried.
Queue reads fail when Postgres is unreachable or the pool is starved; check
`FaasDBPoolStarved` for apid. Failed monitor evaluations are deferred and
retried; retried route checks back off up to `RouteCheckRetryMax` and give up
after `RouteCheckMaxAttempts`. The apid warnings name the app but never the
error detail, which can contain private intent.

## Recover

The workers need no manual reset. Once Postgres is healthy, the next drain or
run catches up: overdue monitors are evaluated oldest first, and the insights
evaluator takes the least recently evaluated apps first.
