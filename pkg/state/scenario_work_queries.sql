-- name: CloseScenarioManagedWork :exec
UPDATE scenario_test_action_policies SET managed_work_closing=true
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text AND managed_work_plan IS NOT NULL;

-- name: ScenarioManagedWorkJobs :many
SELECT document,payload FROM scenario_managed_work
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text ORDER BY invocation_id LIMIT 65;

-- name: ScenarioManagedWorkBindings :many
SELECT p.app_id::text AS app_id,p.workload_name,
 (SELECT count(*) FROM deployments d WHERE d.app_id=p.app_id)::bigint AS deployments,
 COALESCE((SELECT d.id::text FROM deployments d WHERE d.app_id=p.app_id LIMIT 1),'')::text AS deployment_id,
 COALESCE((SELECT f.id::text FROM scenario_postgres_fixtures f WHERE f.account_id=p.account_id AND f.run_id=p.run_id AND f.status='ready' AND f.expires_at>now()),'')::text AS postgres_resource_id,
 COALESCE((SELECT f.id::text FROM scenario_object_fixtures f WHERE f.account_id=p.account_id AND f.run_id=p.run_id AND f.status='ready' AND f.expires_at>now()),'')::text AS objects_resource_id,
 p.managed_work_failed
FROM scenario_test_action_policies p
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text ORDER BY p.workload_name;

-- name: ScenarioManagedWorkInventoryValid :one
WITH members AS (SELECT app_id FROM scenario_test_action_policies WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text),
 jobs AS (SELECT * FROM scenario_managed_work WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text)
SELECT (SELECT count(*) FROM invocations i JOIN members m ON m.app_id=i.app_id)=(SELECT count(*) FROM jobs)
 AND NOT EXISTS(SELECT 1 FROM invocations i JOIN members m ON m.app_id=i.app_id LEFT JOIN jobs j ON j.invocation_id=i.id
 WHERE j.invocation_id IS NULL OR j.app_id<>i.app_id OR j.account_id<>i.account_id OR j.payload IS DISTINCT FROM COALESCE(i.payload,'null'::jsonb)
 OR j.document->>'id'<>i.id::text OR j.document->>'app_id'<>i.app_id::text OR j.document->>'method'<>i.method OR j.document->>'path'<>i.path
 OR j.document->>'state'<>i.state OR NOT(jsonb_array_length(j.document->'attempts')=i.attempts
  OR (i.state='dispatching' AND nullif(i.instance_id,'') IS NULL AND jsonb_array_length(j.document->'attempts')=i.attempts-1))
 OR i.source<>'async_invoke') AS valid;
