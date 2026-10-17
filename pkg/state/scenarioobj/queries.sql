-- name: LockRun :exec
SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('scenario-action-run:' || sqlc.arg(account_id)::text || ':' || sqlc.arg(run_id)::text,0));

-- name: Reserve :one
INSERT INTO scenario_object_fixtures(id,account_id,run_id,app_id,status,expires_at,document)
SELECT sqlc.arg(id)::uuid,p.account_id,p.run_id,p.app_id,'ready',sqlc.arg(expires_at)::timestamptz,sqlc.arg(document)::jsonb
FROM scenario_test_action_policies p JOIN apps a ON a.id=p.app_id
WHERE p.account_id=sqlc.arg(account_id)::uuid AND p.run_id=sqlc.arg(run_id) AND p.app_id=sqlc.arg(app_id)::uuid
 AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL
 AND NOT p.recorder_closing AND NOT p.simulation_failed AND a.status<>'deleted' AND a.preview_expires_at>now()
 AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id)
 AND (p.managed_work_plan IS NULL OR (p.managed_work_plan->>'primary'=p.workload_name AND EXISTS(SELECT 1 FROM jsonb_array_elements(p.managed_work_plan->'participants') v WHERE v->>'workload'=p.workload_name AND v->>'objects'='read_write')))
RETURNING document;

-- name: Get :one
SELECT document FROM scenario_object_fixtures WHERE account_id=sqlc.arg(account_id)::uuid AND run_id=sqlc.arg(run_id);

-- name: GetLocked :one
SELECT document FROM scenario_object_fixtures WHERE account_id=sqlc.arg(account_id)::uuid AND run_id=sqlc.arg(run_id) FOR UPDATE;

-- name: GetForApp :one
SELECT f.document,CASE WHEN p.managed_work_plan IS NOT NULL THEN p.workload_name ELSE '' END::text AS caller_workload,
 COALESCE((SELECT v->>'objects' FROM jsonb_array_elements(p.managed_work_plan->'participants') v WHERE v->>'workload'=p.workload_name),'read_write')::text AS caller_permission,
 CASE WHEN p.managed_work_plan IS NOT NULL THEN COALESCE((SELECT d.id::text FROM deployments d WHERE d.app_id=p.app_id LIMIT 1),'') ELSE '' END::text AS caller_deployment_id
FROM scenario_object_fixtures f JOIN scenario_test_action_policies p ON p.account_id=f.account_id AND p.run_id=f.run_id
 JOIN apps a ON a.id=p.app_id
WHERE p.app_id=sqlc.arg(app_id)::uuid
 AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL
 AND NOT p.recorder_closing AND NOT p.simulation_failed AND a.status<>'deleted' AND a.preview_expires_at>now()
 AND f.status='ready' AND f.expires_at>now()
 AND COALESCE(f.document->>'CollectorID','')<>'' AND NOT COALESCE((f.document->>'CollectorClosed')::boolean,false)
 AND ((p.managed_work_plan IS NULL AND f.app_id=p.app_id) OR
 (p.managed_work_plan IS NOT NULL AND (SELECT count(*) FROM deployments d WHERE d.app_id=p.app_id)=1
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(p.managed_work_plan->'participants') v WHERE v->>'workload'=p.workload_name AND v->>'objects' IN ('read','read_write'))));

-- name: Active :many
SELECT document FROM scenario_object_fixtures
WHERE status IN ('ready','sealing') AND expires_at>now()
 AND COALESCE((document->>'CollectorClosed')::boolean,false)=false
 AND (sqlc.narg(after_id)::uuid IS NULL OR id>sqlc.narg(after_id)::uuid)
ORDER BY id LIMIT 16;

-- name: Update :exec
UPDATE scenario_object_fixtures SET document=sqlc.arg(document)::jsonb,status=sqlc.arg(status)
WHERE id=sqlc.arg(id)::uuid;

-- name: Expired :many
SELECT document FROM scenario_object_fixtures WHERE status IN ('ready','sealing') AND expires_at<now() ORDER BY expires_at LIMIT 16;
