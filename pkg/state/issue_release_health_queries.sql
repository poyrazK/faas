-- name: IssueReleaseHealth :one
-- Independent aggregates avoid multiplying request weights by issue occurrences.
SELECT d.id AS deployment_id,d.commit_sha,d.created_at,
 requests.observed_requests,requests.http_5xx_requests,
 events.observed_issue_events,events.observed_issue_groups,events.identified_customers,events.unattributed_events,
 activity.new_issue_groups,activity.regressed_issue_groups
FROM deployments d JOIN apps app ON app.id=d.app_id
CROSS JOIN LATERAL (
 SELECT COALESCE(sum(t.count),0)::bigint AS observed_requests,
 COALESCE(sum(t.count) FILTER(WHERE t.status>=500),0)::bigint AS http_5xx_requests
 FROM request_telemetry t WHERE t.app_id=app.id AND t.account_id=app.account_id AND t.deployment_id=d.id
 AND t.received_at>=sqlc.arg(window_start)::timestamptz AND t.received_at<sqlc.arg(window_end)::timestamptz
) requests
CROSS JOIN LATERAL (
 SELECT count(*) AS observed_issue_events,count(DISTINCT a.canonical_issue_id) AS observed_issue_groups,
 count(DISTINCT COALESCE(e.verified_platform_tenant_id,e.verified_consumer_id)) AS identified_customers,
 count(*) FILTER(WHERE e.verified_platform_tenant_id IS NULL AND e.verified_consumer_id IS NULL) AS unattributed_events
 FROM issue_events e JOIN app_issues i ON i.id=e.issue_id JOIN issue_group_assignments a ON a.original_issue_id=e.issue_id
 WHERE e.app_id=app.id AND e.deployment_id=d.id AND i.environment=sqlc.arg(environment)::text
 AND e.occurred_at>=sqlc.arg(window_start)::timestamptz AND e.occurred_at<sqlc.arg(window_end)::timestamptz
) events
CROSS JOIN LATERAL (
 SELECT count(DISTINCT a.canonical_issue_id) FILTER(WHERE act.action='created') AS new_issue_groups,
 count(DISTINCT a.canonical_issue_id) FILTER(WHERE act.action='regressed') AS regressed_issue_groups
 FROM issue_activity act JOIN app_issues i ON i.id=act.issue_id JOIN issue_group_assignments a ON a.original_issue_id=i.id
 WHERE i.app_id=app.id AND i.environment=sqlc.arg(environment)::text AND act.details->>'deployment_id'=d.id::text
 AND act.created_at>=sqlc.arg(window_start)::timestamptz AND act.created_at<sqlc.arg(window_end)::timestamptz
) activity
WHERE d.app_id=sqlc.arg(app_id)::uuid AND app.account_id=sqlc.arg(account_id)::uuid AND d.id=sqlc.arg(deployment_id)::uuid;
