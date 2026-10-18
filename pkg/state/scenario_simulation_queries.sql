-- name: ScenarioSimulationPolicy :one
SELECT p.account_id::text AS account_id,p.run_id,p.workload_name,p.simulation_plan,p.sealed_at,p.recorder_closing,
 coalesce((SELECT deployment_id::text FROM instances WHERE id=sqlc.arg(instance_id)::text::uuid),
 (SELECT deployment_id::text FROM app_tasks WHERE id=sqlc.arg(instance_id)::text::uuid),'')::text AS deployment_id,
 p.account_id=sqlc.arg(account_id)::text::uuid AND a.account_id=p.account_id
 AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND NOT p.simulation_failed
 AND a.status<>'deleted' AND a.preview_of_slug IS NOT NULL AND a.preview_pr_state='open'
 AND a.preview_pr_number=0 AND a.preview_expires_at>clock_timestamp() AND p.sealed_at IS NULL
 AND scenario_rehearsal_execution_active(p.app_id,sqlc.arg(instance_id)::text::uuid,NULL)
 AND EXISTS(SELECT 1 FROM scenario_test_members m WHERE m.app_id=p.app_id AND m.account_id=p.account_id AND m.run_id=p.run_id) AS active
FROM scenario_test_action_policies p JOIN apps a ON a.id=p.app_id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid;

-- name: ScenarioSimulationRunAttempts :many
SELECT e.id::text AS id,e.app_id::text AS app_id,e.evidence
FROM scenario_simulation_attempts e JOIN scenario_test_action_policies p ON p.app_id=e.app_id
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text
ORDER BY e.created_at,e.id LIMIT 257;

-- name: InsertScenarioSimulationAttempt :exec
INSERT INTO scenario_simulation_attempts(id,app_id,evidence)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(evidence)::jsonb);

-- name: FinishScenarioSimulationAttempt :execrows
UPDATE scenario_simulation_attempts SET evidence=jsonb_set(evidence,'{status}',to_jsonb(sqlc.arg(status)::text))
WHERE id=sqlc.arg(id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid AND evidence->>'status'='pending';

-- name: FailScenarioSimulation :exec
UPDATE scenario_test_action_policies SET simulation_failed=true
WHERE app_id=sqlc.arg(app_id)::text::uuid AND simulation_plan IS NOT NULL AND sealed_at IS NULL;

-- name: ScenarioRehearsalSimulationRun :one
SELECT run_id FROM scenario_test_action_policies
WHERE app_id=sqlc.arg(app_id)::text::uuid AND network_mode='contained' AND simulation_plan IS NOT NULL;
