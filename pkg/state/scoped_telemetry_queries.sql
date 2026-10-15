-- name: ListScopedObservedRoutes :many
SELECT DISTINCT (t.method || ' ' || t.route)::text AS route
FROM request_telemetry t JOIN deployments d ON d.id=t.deployment_id AND d.app_id=t.app_id
JOIN apps a ON a.id=d.app_id AND a.account_id=t.account_id
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
AND d.scope=sqlc.arg(scope) AND t.received_at>=sqlc.arg(since) AND t.received_at<=sqlc.arg(until)
AND t.route<>'' AND t.method<>''
ORDER BY route LIMIT sqlc.arg(row_limit);

-- name: ListScopedPendingRollbacks :many
SELECT r.receipt FROM deployment_rollback_operations r JOIN apps a ON a.id=r.app_id
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
AND r.scope=sqlc.arg(scope) AND r.status NOT IN ('complete','failed')
ORDER BY r.updated_at DESC,r.id DESC LIMIT sqlc.arg(row_limit);

-- name: ScopedOpenMonitorIncident :one
SELECT i.id,i.deployment_id,i.opened_at FROM route_monitor_incidents i
JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
JOIN deployments d ON d.id=i.deployment_id AND d.app_id=a.id
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
AND d.scope=sqlc.arg(scope) AND i.status='open' ORDER BY i.opened_at DESC,i.id DESC LIMIT 1;

-- name: ListScopedPendingRestarts :many
SELECT (o.payload::jsonb->>'wake_id')::text AS wake_id,
 CASE o.state WHEN 'pending' THEN CASE WHEN o.attempts>0 THEN 'retrying' ELSE 'queued' END
 WHEN 'processing' THEN 'running' WHEN 'dead_letter' THEN 'failed' ELSE 'unknown' END::text AS status,
 o.attempts,CASE WHEN COALESCE(o.last_error,'')='' THEN ''
 WHEN position('reason=telemetry_missing' in o.last_error)>0 THEN 'telemetry_missing'
 WHEN position('reason=requests_active' in o.last_error)>0 THEN 'requests_active'
 WHEN position('reason=quiet_period_not_elapsed' in o.last_error)>0 THEN 'quiet_period_not_elapsed'
 ELSE 'restart_attempt_failed' END::text AS failure_reason,
 o.created_at AS requested_at,o.delivered_at AS completed_at
FROM notification_outbox o JOIN apps a ON a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
WHERE o.channel='runtime_config_restart' AND o.payload::jsonb->>'app_id'=a.id::text
AND COALESCE(o.payload::jsonb->>'wake_id','')<>'' AND o.payload::jsonb->>'scope'=sqlc.arg(scope)
AND o.state<>'delivered'
AND NOT EXISTS(SELECT 1 FROM notification_outbox newer WHERE newer.channel=o.channel
 AND newer.payload::jsonb->>'app_id'=o.payload::jsonb->>'app_id'
 AND newer.payload::jsonb->>'wake_id'=o.payload::jsonb->>'wake_id' AND newer.id>o.id)
ORDER BY o.created_at DESC,o.id DESC LIMIT sqlc.arg(row_limit);

-- name: ReadWorkerRolloutObservation :one
WITH candidate AS (
 SELECT d.id,d.app_id,d.scope,d.status FROM deployments d JOIN apps a ON a.id=d.app_id
 WHERE d.id=sqlc.arg(deployment_id) AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
), selected AS (
 SELECT d.id FROM deployments d JOIN candidate c ON c.app_id=d.app_id AND c.scope=d.scope
 WHERE d.status='live' ORDER BY d.created_at DESC,d.id DESC LIMIT 1
), residents AS (
 SELECT i.deployment_id,i.state FROM instances i JOIN deployments d ON d.id=i.deployment_id AND d.app_id=i.app_id
 JOIN candidate c ON c.app_id=d.app_id AND c.scope=d.scope
 WHERE i.mode='worker' AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
)
SELECT c.id::text AS deployment_id,c.scope,c.status,
 coalesce((SELECT id::text FROM selected),'')::text AS selected_deployment_id,
 (SELECT count(*) FROM residents WHERE deployment_id=c.id AND state='running')::bigint AS running,
 (SELECT count(*) FROM residents WHERE deployment_id=c.id AND state IN ('waking','cold_booting'))::bigint AS starting,
 (SELECT count(*) FROM residents WHERE deployment_id=c.id AND state='draining')::bigint AS draining,
 (SELECT count(*) FROM residents WHERE deployment_id<>c.id)::bigint AS retired_residents,
 (SELECT count(*) FROM residents WHERE deployment_id=c.id AND state IN ('snapshotting','migrating','warm'))::bigint AS other_residents
FROM candidate c;

-- name: ScopedRouteMonitorServingDeployments :many
SELECT d.id::text AS id,d.commit_sha,d.source_url,d.source_root,d.created_at,d.canary_step_started_at,d.rollout_completed_at,d.traffic_percent,d.canary_step,d.canary_total_steps
FROM deployments d JOIN apps a ON a.id=d.app_id
WHERE a.id=sqlc.arg(app_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.status<>'deleted'
AND d.status='live' AND d.deleted_at IS NULL AND d.traffic_percent>0
AND d.scope=sqlc.arg(scope)
AND (d.scope='default' OR EXISTS (
 SELECT 1 FROM project_environment_workload_deployment_specs ds
 JOIN project_environment_workload_specs spec ON spec.id=ds.spec_id
 JOIN project_environments e ON e.id=spec.environment_id
 WHERE ds.deployment_id=d.id AND spec.app_id=a.id AND e.project_id=a.project_id
 AND e.account_id=a.account_id AND e.slug=d.scope
)) ORDER BY d.id LIMIT 2;

-- name: ReadEnvironmentBudgetIdentity :one
SELECT e.id::text AS environment_id, e.project_id::text AS project_id
FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' AND e.slug=sqlc.arg(scope)::text;

-- name: LockEnvironmentBudgetIdentity :one
SELECT e.id::text AS environment_id, e.project_id::text AS project_id
FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' AND e.slug=sqlc.arg(scope)::text FOR SHARE OF e;

-- name: ReadEnvironmentRouteBudgets :one
SELECT to_jsonb(b) FROM app_environment_route_budgets b
WHERE b.app_id=sqlc.arg(app_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid AND b.scope=sqlc.arg(scope)::text
AND coalesce(b.environment_id::text,'')=sqlc.arg(environment_id)::text AND coalesce(b.project_id::text,'')=sqlc.arg(project_id)::text;

-- name: WriteEnvironmentRouteBudgets :exec
INSERT INTO app_environment_route_budgets(account_id,app_id,scope,project_id,environment_id,inherit,enabled,revision,routes)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,nullif(sqlc.arg(project_id)::text,'')::uuid,nullif(sqlc.arg(environment_id)::text,'')::uuid,sqlc.arg(inherit)::boolean,sqlc.arg(enabled)::boolean,sqlc.arg(revision)::bigint,sqlc.arg(routes)::jsonb)
ON CONFLICT(app_id,scope) DO UPDATE SET project_id=excluded.project_id,environment_id=excluded.environment_id,inherit=excluded.inherit,enabled=excluded.enabled,revision=excluded.revision,routes=excluded.routes,updated_at=clock_timestamp();

-- name: ReadEnvironmentBudgetReceipt :one
SELECT request_hash,response FROM environment_route_budget_receipts
WHERE account_id=sqlc.arg(account_id)::uuid AND request_id=sqlc.arg(request_id)::uuid;

-- name: WriteEnvironmentBudgetReceipt :exec
INSERT INTO environment_route_budget_receipts(account_id,request_id,app_id,request_hash,response)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(request_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(request_hash)::text,sqlc.arg(response)::jsonb);
