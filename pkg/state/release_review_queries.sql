-- name: ReadReleaseReview :one
SELECT a.id::text AS app_id,
       encode(sha256(convert_to(((to_jsonb(a) - ARRAY[
           'last_scale_out_at', 'last_scale_in_at', 'node_id', 'reassigned_at',
           'parked_deployment_id', 'parked_deployment'
       ]) || CASE WHEN sqlc.arg(scope)::text = 'default' THEN '{}'::jsonb ELSE
       jsonb_build_object('environment',to_jsonb(e),'spec',to_jsonb(s),'route',to_jsonb(r)) END)::text, 'UTF8')), 'hex') AS configuration_hash,
	   sqlc.arg(scope)::text AS scope,
	   CASE WHEN sqlc.arg(scope)::text = 'default' THEN '' ELSE a.project_id::text END::text AS project_id,
	   coalesce(e.id::text,'')::text AS environment_id,
	   coalesce(s.revision,0)::bigint AS workload_revision,
	   coalesce(s.config_hash,'')::text AS workload_config_hash,
	   coalesce(e.protected,false)::boolean AS protected,
       coalesce((SELECT d.id::text FROM deployments d
                 WHERE d.app_id=a.id AND d.scope=sqlc.arg(scope)::text
                 ORDER BY d.created_at DESC, d.id DESC LIMIT 1), '')::text AS latest_deployment_id,
       ARRAY(SELECT d.id::text FROM deployments d
             WHERE d.app_id=a.id AND d.scope=sqlc.arg(scope)::text
               AND d.status='live' AND d.traffic_percent>0 ORDER BY d.id)::text[] AS serving_deployment_ids
FROM apps a
LEFT JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=sqlc.arg(scope)::text
LEFT JOIN project_environment_workload_heads h ON h.environment_id=e.id AND h.app_id=a.id
LEFT JOIN project_environment_workload_specs s ON s.id=h.spec_id
LEFT JOIN project_environment_route_policies r ON r.app_id=a.id AND r.environment_slug=e.slug
WHERE a.id=sqlc.arg(app_id)::uuid AND a.status <> 'deleted'
  AND (sqlc.arg(scope)::text='default' OR e.id IS NOT NULL);

-- name: LockReviewedEnvironment :one
SELECT e.id::text FROM project_environments e JOIN apps a
 ON a.project_id=e.project_id AND a.account_id=e.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND e.slug=sqlc.arg(scope)::text AND a.status <> 'deleted'
FOR UPDATE OF e,a;

-- name: LockReviewedRoutePolicy :exec
SELECT 1 FROM project_environment_route_policies
WHERE app_id=sqlc.arg(app_id)::uuid AND environment_slug=sqlc.arg(scope)::text FOR UPDATE;

-- name: ReadDeploymentTrafficScope :one
SELECT app_id::text,scope FROM deployments WHERE id=sqlc.arg(id)::uuid;

-- name: ListTrafficSiblingsInScope :many
SELECT id::text,traffic_percent FROM deployments
WHERE app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text AND status='live' AND id<>sqlc.arg(id)::uuid ORDER BY id;

-- name: ReadLiveTrafficSumInScope :one
SELECT coalesce(sum(traffic_percent),0)::bigint FROM deployments
WHERE app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text AND status='live';
