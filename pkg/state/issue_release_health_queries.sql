-- name: IssueReleaseHealth :one
-- Independent aggregates avoid multiplying request weights by issue occurrences.
WITH impact_events AS MATERIALIZED (
 SELECT a.canonical_issue_id,CASE WHEN e.occurred_at >= sqlc.arg(window_start)::timestamptz AND e.occurred_at < sqlc.arg(window_end)::timestamptz THEN e.verified_platform_tenant_id END AS verified_platform_tenant_id, CASE WHEN e.occurred_at >= sqlc.arg(window_start)::timestamptz AND e.occurred_at < sqlc.arg(window_end)::timestamptz THEN e.verified_consumer_id END AS verified_consumer_id, issue_window_weight(e.occurred_at,e.occurrence_count,e.http_time_buckets,sqlc.arg(window_start)::timestamptz,sqlc.arg(window_end)::timestamptz,true,false)::bigint AS occurrence_count
 FROM issue_events e JOIN app_issues i ON i.id=e.issue_id JOIN issue_group_assignments a ON a.original_issue_id=e.issue_id
 WHERE e.app_id=sqlc.arg(app_id)::uuid AND e.deployment_id=sqlc.arg(deployment_id)::uuid AND i.environment=sqlc.arg(environment)::text
 AND e.occurred_at>=sqlc.arg(window_start)::timestamptz - interval '1 second' AND e.occurred_at<sqlc.arg(window_end)::timestamptz + interval '1 second'
 AND issue_window_weight(e.occurred_at,e.occurrence_count,e.http_time_buckets,sqlc.arg(window_start)::timestamptz,sqlc.arg(window_end)::timestamptz,true,false)>0
), linked_customers AS (
 SELECT DISTINCT verified_consumer_id FROM impact_events WHERE verified_consumer_id IS NOT NULL AND verified_platform_tenant_id IS NOT NULL
)
SELECT d.id AS deployment_id,d.commit_sha,d.created_at,
 requests.observed_requests,requests.http_5xx_requests,
 events.observed_issue_events,events.observed_issue_groups,events.identified_customers,events.unattributed_events,unknown_events.unknown_http_batches,
 activity.new_issue_groups,activity.regressed_issue_groups
FROM deployments d JOIN apps app ON app.id=d.app_id
CROSS JOIN LATERAL (
 SELECT COALESCE(sum(t.count),0)::bigint AS observed_requests,
 COALESCE(sum(t.count) FILTER(WHERE t.status>=500),0)::bigint AS http_5xx_requests
 FROM request_telemetry t WHERE t.app_id=app.id AND t.account_id=app.account_id AND t.deployment_id=d.id
 AND t.received_at>=sqlc.arg(window_start)::timestamptz AND t.received_at<sqlc.arg(window_end)::timestamptz
) requests
CROSS JOIN LATERAL (
 SELECT COALESCE(sum(e.occurrence_count),0)::bigint AS observed_issue_events,count(DISTINCT e.canonical_issue_id) AS observed_issue_groups,
 (count(DISTINCT e.verified_platform_tenant_id) + count(DISTINCT e.verified_consumer_id)
 FILTER (WHERE e.verified_platform_tenant_id IS NULL AND l.verified_consumer_id IS NULL))::bigint AS identified_customers,
 COALESCE(sum(e.occurrence_count - CASE WHEN e.verified_platform_tenant_id IS NOT NULL OR e.verified_consumer_id IS NOT NULL THEN 1 ELSE 0 END),0)::bigint AS unattributed_events
 FROM impact_events e LEFT JOIN linked_customers l ON l.verified_consumer_id=e.verified_consumer_id
) events
CROSS JOIN LATERAL (
 SELECT count(*)::bigint AS unknown_http_batches
 FROM issue_events legacy JOIN app_issues li ON li.id=legacy.issue_id
 WHERE legacy.app_id=sqlc.arg(app_id)::uuid AND legacy.deployment_id=sqlc.arg(deployment_id)::uuid AND li.environment=sqlc.arg(environment)::text
 AND legacy.occurred_at>=sqlc.arg(window_start)::timestamptz AND legacy.occurred_at<sqlc.arg(window_end)::timestamptz
 AND NOT legacy.occurrence_weight_known AND legacy.occurrence_count=1 AND legacy.payload->>'source_kind'='http'
) unknown_events
CROSS JOIN LATERAL (
 SELECT count(DISTINCT a.canonical_issue_id) FILTER(WHERE act.action='created') AS new_issue_groups,
 count(DISTINCT a.canonical_issue_id) FILTER(WHERE act.action='regressed') AS regressed_issue_groups
 FROM issue_activity act JOIN app_issues i ON i.id=act.issue_id JOIN issue_group_assignments a ON a.original_issue_id=i.id
 WHERE i.app_id=app.id AND i.environment=sqlc.arg(environment)::text AND act.details->>'deployment_id'=d.id::text
 AND act.created_at>=sqlc.arg(window_start)::timestamptz AND act.created_at<sqlc.arg(window_end)::timestamptz
) activity
WHERE d.app_id=sqlc.arg(app_id)::uuid AND app.account_id=sqlc.arg(account_id)::uuid AND d.id=sqlc.arg(deployment_id)::uuid;
