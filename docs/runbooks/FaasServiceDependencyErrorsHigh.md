# FaasServiceDependencyErrorsHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`service_dependency`.

Metric: `gateway_service_dependency_edge_calls_total{caller_app,target_app,outcome}`.
The gateway records final managed-call outcomes for trusted service identities;
identity and authorization failures are excluded. An `error` is a final 5xx
response or a failed native gRPC status.

Severity: warn. Family: `service_dependency`.

## Symptom

A caller-to-target edge has had more than 10% errors over ten minutes, with at
least five errors and twenty calls, for five minutes. The alert labels identify
the caller and target by app UUID. The volume floors suppress pages for sparse
edges and isolated failures.

The dependency duration histogram includes routing, target wake, forwarding,
and retries. A high p95 can therefore point to a slow wake or retry path even
when the final error ratio is low; compare it with the caller's configured
service timeout and workload SLO rather than treating one latency threshold as
universal.

## Verify

Replace `<caller_app_uuid>` and `<target_app_uuid>` with the values from the
alert. These queries use the Prometheus endpoint on the monitoring host:

```bash
# Calls and final error ratio for the alerting edge.
curl -fsS --data-urlencode 'query=sum by (caller_app,target_app) (increase(gateway_service_dependency_edge_calls_total{caller_app="<caller_app_uuid>",target_app="<target_app_uuid>"}[10m]))' \
  http://127.0.0.1:9095/api/v1/query | jq .
curl -fsS --data-urlencode 'query=sum by (caller_app,target_app) (increase(gateway_service_dependency_edge_calls_total{caller_app="<caller_app_uuid>",target_app="<target_app_uuid>",outcome="error"}[10m])) / clamp_min(sum by (caller_app,target_app) (increase(gateway_service_dependency_edge_calls_total{caller_app="<caller_app_uuid>",target_app="<target_app_uuid>"}[10m])),1)' \
  http://127.0.0.1:9095/api/v1/query | jq .

# End-to-end dependency latency, including cold wake and retry time.
curl -fsS --data-urlencode 'query=histogram_quantile(0.95,sum by (le,caller_app,target_app) (rate(gateway_service_dependency_duration_seconds_bucket{caller_app="<caller_app_uuid>",target_app="<target_app_uuid>"}[10m])))' \
  http://127.0.0.1:9095/api/v1/query | jq .
```

Resolve the UUIDs to app slugs using a read-only database connection:

```sql
SELECT id, slug
FROM apps
WHERE id IN ('<caller_app_uuid>', '<target_app_uuid>');
```

Then inspect the caller's recent deploy and service binding, the target's
deployment and compute-node health, and the trace for a failing call. The
service-proxy span includes `gregale.service.name`,
`gregale.service.caller_app_id`, `gregale.service.target_app_id`, and
`http.response.status_code`. Match both app IDs from the alert to identify the
exact edge. In the fleet dashboard, compare the edge's error ratio and call
volume, then check its p95 latency. The dependency-latency heatmap can link a
sampled call to its trace through the `trace_id` exemplar; unsampled calls still
contribute to the edge counters and latency histogram. Check whether the target
returned a 5xx or whether native gRPC returned a non-zero terminal status.

## Recover

1. If the target is unhealthy, repair or roll back its deployment and restore
   compute-node or VMMD health. Confirm that wake and forwarding complete for
   a direct request.
2. If failures began with a caller release, roll back the caller or correct its
   service binding and timeout/retry policy. Keep retry limits bounded while
   the target is failing.
3. If p95 duration is high but calls eventually succeed, determine whether
   cold wake or retries explain the latency. Compare it with that caller's
   timeout and workload SLO, then fix the slow stage or adjust the caller's
   policy deliberately.
4. Re-run the call and watch the edge's error ratio and p95. Prometheus resolves
   the alert when its error-ratio or volume conditions stop holding; the
   five-minute pending period delays initial firing while the condition holds.

Do not increase retries to hide a target outage. Retries can increase load on
the same failing dependency.
