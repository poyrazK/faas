-- name: AppConnectionBudgetDatabaseIDs :many
SELECT DISTINCT b.database_id::text AS database_id
FROM managed_postgres_bindings b
JOIN managed_postgres_databases d ON d.id=b.database_id AND d.account_id=b.account_id
WHERE b.account_id=sqlc.arg(account_id)::text::uuid AND b.app_id=sqlc.arg(app_id)::text::uuid
 AND b.access IN ('data_api','read_only')
 AND (b.state<>'deleted' OR EXISTS(SELECT 1 FROM instances i LEFT JOIN deployments dep ON dep.id=i.deployment_id
   WHERE i.app_id=b.app_id AND i.state NOT IN ('stopped','failed')
    AND (dep.id IS NULL OR coalesce(nullif(dep.scope,''),'default')=b.scope)))
 AND d.clone_resource_role='target'
 AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired')
 AND (d.environment_clone_operation_id IS NULL OR EXISTS(
   SELECT 1 FROM project_environment_clone_operations o WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
    AND EXISTS(SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
      WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
    AND EXISTS(SELECT 1 FROM jsonb_array_elements(o.resources) r WHERE r->>'kind' IN ('postgres','managed_postgres')
      AND r->>'source_id'=d.restore_source_database_id::text AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
ORDER BY database_id LIMIT sqlc.arg(page_limit);

-- name: ReadConnectionBudgetSnapshot :one
WITH owned AS (
 SELECT d.* FROM managed_postgres_databases d
 WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid AND d.clone_resource_role='target'
  AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired')
  AND (d.environment_clone_operation_id IS NULL OR EXISTS(
    SELECT 1 FROM project_environment_clone_operations o WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
     AND EXISTS(SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
       WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
     AND EXISTS(SELECT 1 FROM jsonb_array_elements(o.resources) r WHERE r->>'kind' IN ('postgres','managed_postgres')
       AND r->>'source_id'=d.restore_source_database_id::text AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
), candidates AS (
 SELECT b.id,b.app_id,b.scope,b.access,b.state,b.rotation_previous_generation,a.max_concurrency,acct.plan::text AS plan
 FROM owned d JOIN managed_postgres_bindings b ON b.database_id=d.id AND b.account_id=d.account_id
 JOIN apps a ON a.id=b.app_id AND a.account_id=b.account_id JOIN accounts acct ON acct.id=a.account_id
 WHERE b.state<>'deleted' OR EXISTS(SELECT 1 FROM instances i LEFT JOIN deployments dep ON dep.id=i.deployment_id
  WHERE i.app_id=b.app_id AND i.state NOT IN ('stopped','failed')
   AND (dep.id IS NULL OR coalesce(nullif(dep.scope,''),'default')=b.scope))
), selected AS (
 SELECT * FROM candidates ORDER BY id LIMIT sqlc.arg(page_limit)
), consumers AS (
 SELECT b.id,jsonb_build_object('app_id',b.app_id,'scope',b.scope,'access',b.access,'state',b.state,'plan',b.plan,
  'max_concurrency',b.max_concurrency,'rotation',b.rotation_previous_generation IS NOT NULL,
  'residents',inventory.residents,'parked',inventory.parked,'auxiliary',inventory.auxiliary,
  'stale_nodes',inventory.stale_nodes,'deployments',inventory.deployments,'sidecars',inventory.sidecars,'migrating',inventory.migrating,
  'tasks',(SELECT count(*) FROM app_tasks t WHERE t.app_id=b.app_id AND t.account_id=sqlc.arg(account_id)::text::uuid
    AND coalesce(nullif(t.deployment_scope,''),'default')=b.scope AND t.status NOT IN ('succeeded','failed','cancelled','skipped'))) AS item
 FROM selected b CROSS JOIN LATERAL (
  SELECT count(*) FILTER(WHERE i.kind='wake' AND i.mode='normal' AND dep.id IS NOT NULL AND i.state<>'parked') AS residents,
   count(*) FILTER(WHERE i.kind='wake' AND i.mode='normal' AND dep.id IS NOT NULL AND i.state='parked') AS parked,
   count(*) FILTER(WHERE i.kind<>'wake' OR i.mode<>'normal' OR dep.id IS NULL) AS auxiliary,
   count(*) FILTER(WHERE n.id IS NULL OR n.last_heartbeat_at>statement_timestamp()
     OR n.last_heartbeat_at<statement_timestamp()-make_interval(secs=>sqlc.arg(heartbeat_seconds)::integer)) AS stale_nodes,
   count(DISTINCT dep.id) FILTER(WHERE i.state<>'parked' AND i.kind='wake' AND i.mode='normal') AS deployments,
   coalesce(bool_or(jsonb_array_length(coalesce(dep.sidecars,'[]'::jsonb))>0),false) AS sidecars,
   coalesce(bool_or(i.state='migrating' OR i.migrated_from_node_id IS NOT NULL),false) AS migrating
  FROM instances i LEFT JOIN deployments dep ON dep.id=i.deployment_id AND dep.app_id=b.app_id
  LEFT JOIN compute_nodes n ON n.id=i.node_id
  WHERE i.app_id=b.app_id AND i.state NOT IN ('stopped','failed')
   AND (dep.id IS NULL OR coalesce(nullif(dep.scope,''),'default')=b.scope)
 ) inventory
)
SELECT d.id::text AS database_id,d.name,d.state,d.backend_fingerprint,d.observed_generation,d.data_resource_id,
 statement_timestamp()::timestamptz AS checked_at,
 (SELECT count(*)>sqlc.arg(page_limit)::integer FROM candidates) AS truncated,
 coalesce((SELECT jsonb_agg(item ORDER BY id) FROM consumers),'[]'::jsonb)::jsonb AS bindings
FROM owned d;
