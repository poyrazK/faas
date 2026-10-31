-- name: LockRun :exec
SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('scenario-action-run:' || sqlc.arg(account_id)::text || ':' || sqlc.arg(run_id)::text,0));

-- name: Reserve :one
INSERT INTO scenario_postgres_fixtures(id,account_id,run_id,app_id,owner_id,database_name,role_name,status,spec,expires_at,backend_sha256)
SELECT sqlc.arg(id)::uuid,p.account_id,p.run_id,p.app_id,sqlc.arg(owner_id)::uuid,
 sqlc.arg(database_name),sqlc.arg(role_name),'provisioning',sqlc.arg(spec)::jsonb,sqlc.arg(expires_at)::timestamptz,sqlc.arg(backend_sha256)
FROM scenario_test_action_policies p JOIN apps a ON a.id=p.app_id
WHERE p.account_id=sqlc.arg(account_id)::uuid AND p.run_id=sqlc.arg(run_id) AND p.app_id=sqlc.arg(app_id)::uuid
 AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL
 AND NOT p.recorder_closing AND NOT p.simulation_failed AND a.status<>'deleted' AND a.preview_expires_at>now()
 AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id)
 AND (p.managed_work_plan IS NULL OR (p.managed_work_plan->>'primary'=p.workload_name AND EXISTS(SELECT 1 FROM jsonb_array_elements(p.managed_work_plan->'participants') v WHERE v->>'workload'=p.workload_name AND v->>'postgres'='read_write')))
RETURNING *;

-- name: Get :one
SELECT * FROM scenario_postgres_fixtures WHERE account_id=sqlc.arg(account_id)::uuid AND run_id=sqlc.arg(run_id);

-- name: GetForApp :one
SELECT f.* FROM scenario_postgres_fixtures f JOIN scenario_test_action_policies p ON p.app_id=sqlc.arg(app_id)::uuid
 JOIN apps a ON a.id=p.app_id AND a.status<>'deleted' AND a.preview_expires_at>now()
WHERE f.account_id=p.account_id AND f.run_id=p.run_id AND p.network_mode='contained'
 AND p.simulation_plan IS NOT NULL AND NOT p.simulation_failed AND p.sealed_at IS NULL AND NOT p.recorder_closing
 AND f.status='ready' AND f.expires_at>now()
 AND ((p.managed_work_plan IS NULL AND f.app_id=p.app_id) OR
  (p.managed_work_plan IS NOT NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(p.managed_work_plan->'participants') v WHERE v->>'workload'=p.workload_name AND v->>'postgres'='read_write')));

-- name: Transition :one
UPDATE scenario_postgres_fixtures SET status=sqlc.arg(status),
 initial_snapshot=CASE WHEN sqlc.arg(status)::text='ready' THEN sqlc.arg(initial_snapshot)::jsonb ELSE initial_snapshot END,
 evidence=CASE WHEN sqlc.arg(status)::text='complete' THEN sqlc.arg(evidence)::jsonb ELSE evidence END
WHERE id=sqlc.arg(id)::uuid AND status=sqlc.arg(expected_status) AND owner_id=sqlc.arg(owner_id)::uuid
RETURNING *;

-- name: Expired :many
-- Random order: rows whose drop keeps failing must not occupy every batch and
-- starve newer expiries (ADR-20261019120000).
SELECT * FROM scenario_postgres_fixtures WHERE expires_at<now() AND status NOT IN ('complete','deleted') ORDER BY random() LIMIT 16;
