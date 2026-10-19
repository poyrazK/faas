-- name: ScenarioWorkerFault :one
SELECT id::text,account_id::text,app_id::text,run_id,instance_id::text,deployment_id::text,invocation_id::text,checkpoint_attempt_id::text,recorder_session_id::text,state,(SELECT workload_name FROM scenario_test_action_policies p WHERE p.app_id=f.app_id) AS workload_name FROM scenario_worker_faults f WHERE id=sqlc.arg(id)::text::uuid;

-- name: ScenarioWorkerFaultForApp :one
SELECT id::text,account_id::text,app_id::text,run_id,instance_id::text,deployment_id::text,invocation_id::text,checkpoint_attempt_id::text,recorder_session_id::text,state,(SELECT workload_name FROM scenario_test_action_policies p WHERE p.app_id=f.app_id) AS workload_name FROM scenario_worker_faults f WHERE app_id=sqlc.arg(app_id)::text::uuid;

-- name: ScenarioWorkerFaultsForRun :many
SELECT id::text,account_id::text,app_id::text,run_id,instance_id::text,deployment_id::text,invocation_id::text,checkpoint_attempt_id::text,recorder_session_id::text,state,(SELECT workload_name FROM scenario_test_action_policies p WHERE p.app_id=f.app_id) AS workload_name FROM scenario_worker_faults f WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text LIMIT 2;

-- name: PendingScenarioWorkerFaults :many
SELECT f.id::text AS id,f.account_id::text AS account_id,f.app_id::text AS app_id,f.run_id,f.instance_id::text AS instance_id,f.deployment_id::text AS deployment_id,f.invocation_id::text AS invocation_id,f.checkpoint_attempt_id::text AS checkpoint_attempt_id,f.recorder_session_id::text AS recorder_session_id,f.state,(SELECT workload_name FROM scenario_test_action_policies p WHERE p.app_id=f.app_id) AS workload_name
FROM scenario_worker_faults f JOIN scenario_test_action_policies p ON p.app_id=f.app_id
WHERE p.sealed_at IS NULL AND (f.state='ready' OR (f.state='terminated' AND EXISTS(SELECT 1 FROM instances i WHERE i.id=f.instance_id AND i.state IN ('waking','cold_booting','running','draining'))))
ORDER BY f.created_at LIMIT 64;

-- name: ClaimScenarioWorkerFault :execrows
UPDATE scenario_worker_faults f SET state='claimed' WHERE id=sqlc.arg(id)::text::uuid AND state='ready'
 AND EXISTS(SELECT 1 FROM scenario_test_action_policies p WHERE p.app_id=f.app_id AND p.sealed_at IS NULL);

-- name: ResetScenarioManagedInvocationBinding :exec
UPDATE invocations i SET instance_id=NULL WHERE i.id=sqlc.arg(id)::text::uuid AND i.state='pending'
 AND EXISTS(SELECT 1 FROM scenario_test_action_policies p WHERE p.app_id=i.app_id AND p.managed_work_plan IS NOT NULL);

-- name: FailScenarioWorkerFault :exec
UPDATE scenario_worker_faults SET state='failed' WHERE id=sqlc.arg(id)::text::uuid AND state<>'terminated';

-- name: ScenarioWorkerDispatchBlocked :one
SELECT coalesce(EXISTS(SELECT 1 FROM scenario_worker_faults f WHERE f.app_id=sqlc.arg(app_id)::text::uuid AND
 (f.state<>'terminated' OR EXISTS(SELECT 1 FROM instances i WHERE i.id=f.instance_id AND i.state IN ('waking','cold_booting','running','draining'))))
 OR EXISTS(SELECT 1 FROM scenario_suspend_resumes r WHERE r.app_id=sqlc.arg(app_id)::text::uuid AND r.invocation_id IS NOT NULL AND r.state<>'complete')
 OR EXISTS(SELECT 1 FROM scenario_postgres_faults f WHERE f.app_id=sqlc.arg(app_id)::text::uuid AND f.phase IN ('requested','cutting')),false)::boolean AS blocked;

-- name: ScenarioWorkerCheckpoint :one
SELECT p.account_id::text AS account_id,p.app_id::text AS app_id,p.run_id,p.workload_name,i.deployment_id::text AS deployment_id,i.id::text AS instance_id,v.id::text AS invocation_id,r.id::text AS recorder_session_id
FROM scenario_test_action_policies p JOIN instances i ON i.app_id=p.app_id
JOIN invocations v ON v.app_id=p.app_id AND nullif(v.instance_id,'')::uuid=i.id
JOIN scenario_recorder_sessions r ON r.instance_id=i.id AND r.app_id=p.app_id AND r.deployment_id=i.deployment_id
JOIN scenario_recorder_requests c ON c.session_id=r.id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid AND i.id=sqlc.arg(instance_id)::text::uuid
 AND c.attempt_id=sqlc.arg(attempt_id)::text::uuid AND c.status='admitted' AND r.status='recording'
 AND p.sealed_at IS NULL AND NOT p.recorder_closing AND NOT p.managed_work_failed
 AND p.managed_work_plan->'worker_termination'->>'workload'=p.workload_name
 AND p.managed_work_plan->'worker_termination'->>'checkpoint'=sqlc.arg(checkpoint)::text
 AND scenario_rehearsal_execution_active(p.app_id,i.id,i.deployment_id)
 AND v.source='async_invoke' AND v.state='dispatching' AND v.attempts=1 AND v.lease_expires_at>clock_timestamp()
 AND (SELECT count(*) FROM invocations x WHERE x.app_id=p.app_id)=1
 AND (SELECT count(*) FROM deployments d WHERE d.app_id=p.app_id)=1;

-- name: ArmScenarioWorkerFault :exec
INSERT INTO scenario_worker_faults(id,account_id,app_id,run_id,instance_id,deployment_id,invocation_id,checkpoint_attempt_id,recorder_session_id,state)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(run_id)::text,sqlc.arg(instance_id)::text::uuid,sqlc.arg(deployment_id)::text::uuid,sqlc.arg(invocation_id)::text::uuid,sqlc.arg(checkpoint_attempt_id)::text::uuid,sqlc.arg(recorder_session_id)::text::uuid,'armed');

-- name: ReadyScenarioWorkerFault :execrows
UPDATE scenario_worker_faults f SET state='ready'
WHERE f.id=sqlc.arg(id)::text::uuid AND f.state='armed' AND f.recorder_session_id=sqlc.arg(session_id)::text::uuid
 AND EXISTS(SELECT 1 FROM scenario_simulation_attempts a WHERE a.id=f.checkpoint_attempt_id AND a.app_id=f.app_id AND a.evidence->>'status'='completed');

-- name: FenceScenarioWorkerFault :execrows
UPDATE scenario_worker_faults f SET state='fenced'
WHERE f.id=sqlc.arg(id)::text::uuid AND f.state='claimed' AND f.recorder_session_id=sqlc.arg(session_id)::text::uuid
 AND EXISTS(SELECT 1 FROM invocations v WHERE v.id=f.invocation_id AND v.state='dispatching' AND v.attempts=1 AND nullif(v.instance_id,'')::uuid=f.instance_id AND v.lease_expires_at>clock_timestamp())
 AND (SELECT count(*) FROM invocations v WHERE v.app_id=f.app_id)=1
 AND NOT EXISTS(SELECT 1 FROM scenario_recorder_requests c WHERE c.session_id=f.recorder_session_id AND c.status<>'completed');

-- name: CompleteScenarioWorkerFault :execrows
UPDATE scenario_worker_faults f SET state='terminated' WHERE f.id=sqlc.arg(id)::text::uuid AND f.state='fenced'
 AND f.recorder_session_id=sqlc.arg(session_id)::text::uuid
 AND EXISTS(SELECT 1 FROM scenario_recorder_sessions r WHERE r.id=f.recorder_session_id AND r.status='recording')
 AND NOT EXISTS(SELECT 1 FROM scenario_recorder_requests c WHERE c.session_id=f.recorder_session_id AND c.status<>'completed');
