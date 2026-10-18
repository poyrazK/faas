-- name: ScenarioColdPreparation :one
SELECT c.id::text AS id,c.account_id::text AS account_id,c.app_id::text AS app_id,c.run_id,p.workload_name,c.deployment_id::text AS deployment_id,
 coalesce(c.instance_id::text,'')::text AS instance_id,coalesce(c.recorder_session_id::text,'')::text AS recorder_session_id,c.state,
 coalesce(c.boot_instance_id::text,'')::text AS boot_instance_id,coalesce(c.boot_recorder_session_id::text,'')::text AS boot_recorder_session_id,
 coalesce(c.wake_id,'')::text AS wake_id,coalesce(c.method,'')::text AS method,c.startup_ms
FROM scenario_cold_preparations c JOIN scenario_test_action_policies p ON p.app_id=c.app_id WHERE c.app_id=sqlc.arg(app_id)::text::uuid;

-- name: ScenarioColdInventory :one
SELECT p.cold_start_spec,p.run_id,p.account_id::text AS account_id,p.workload_name,p.sealed_at,p.recorder_closing,p.simulation_failed,
 ARRAY(SELECT d.id::text FROM deployments d WHERE d.app_id=p.app_id)::text[] AS deployments,
 coalesce((SELECT jsonb_agg(jsonb_build_object('id',i.id,'deployment_id',i.deployment_id,'state',i.state,'tail_count',i.tail_count,
 'recorder',coalesce((SELECT r.id::text FROM scenario_recorder_sessions r WHERE r.instance_id=i.id AND r.app_id=i.app_id AND r.status='recording'),'')))
 FROM instances i WHERE i.app_id=p.app_id AND i.state NOT IN ('stopped','parked','failed')),'[]'::jsonb)::jsonb AS instances,
 (EXISTS(SELECT 1 FROM invocations v WHERE v.app_id=p.app_id) OR
 EXISTS(SELECT 1 FROM app_tasks t WHERE t.app_id=p.app_id AND t.status IN ('queued','restoring','running')) OR
 EXISTS(SELECT 1 FROM exclusive_work_operations o WHERE o.app_id=p.app_id AND o.state IN ('pending','running')) OR
 EXISTS(SELECT 1 FROM workflow_runs w WHERE w.app_id=p.app_id AND w.status IN ('pending','running','awaiting_event')) OR
 EXISTS(SELECT 1 FROM scenario_recorder_sessions r WHERE r.app_id=p.app_id AND (r.status='failed' OR EXISTS
 (SELECT 1 FROM scenario_recorder_requests q WHERE q.session_id=r.id AND q.status<>'completed')))) AS busy
FROM scenario_test_action_policies p WHERE p.app_id=sqlc.arg(app_id)::text::uuid;

-- name: InsertScenarioColdPreparation :exec
INSERT INTO scenario_cold_preparations(id,account_id,app_id,run_id,deployment_id,instance_id,recorder_session_id,state)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(run_id)::text,
 sqlc.arg(deployment_id)::text::uuid,nullif(sqlc.arg(instance_id)::text,'')::uuid,nullif(sqlc.arg(session_id)::text,'')::uuid,sqlc.arg(state)::text);

-- name: TransitionScenarioColdPreparation :execrows
UPDATE scenario_cold_preparations c SET state=sqlc.arg(state)::text,
 ready_at=CASE WHEN sqlc.arg(state)::text='ready' THEN clock_timestamp() ELSE ready_at END
WHERE c.app_id=sqlc.arg(app_id)::text::uuid AND c.state=sqlc.arg(expected)::text
 AND EXISTS(SELECT 1 FROM scenario_test_action_policies p WHERE p.app_id=c.app_id AND p.sealed_at IS NULL AND NOT p.recorder_closing);

-- name: RecordScenarioColdBoot :execrows
UPDATE scenario_cold_preparations c SET state='booted',boot_instance_id=sqlc.arg(instance_id)::text::uuid,
 boot_recorder_session_id=sqlc.arg(session_id)::text::uuid,method=sqlc.arg(method)::text,startup_ms=sqlc.arg(startup_ms)::bigint,
 wake_id=(SELECT wake_id::text FROM instances WHERE id=sqlc.arg(instance_id)::text::uuid)
WHERE c.app_id=sqlc.arg(app_id)::text::uuid AND c.state='ready' AND c.deployment_id=sqlc.arg(deployment_id)::text::uuid
 AND c.instance_id IS DISTINCT FROM sqlc.arg(instance_id)::text::uuid;

-- name: FailScenarioColdPreparation :exec
UPDATE scenario_cold_preparations SET state='failed' WHERE app_id=sqlc.arg(app_id)::text::uuid AND state NOT IN ('failed','booted');
