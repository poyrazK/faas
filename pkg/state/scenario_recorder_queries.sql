-- name: StartScenarioRecorder :exec
INSERT INTO scenario_recorder_sessions(id,app_id,instance_id,deployment_id)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(instance_id)::text::uuid,sqlc.arg(deployment_id)::text::uuid);

-- name: ScenarioRecorderSession :one
SELECT r.id::text AS id,r.status,p.recorder_closing,
 (SELECT count(*) FROM scenario_recorder_requests c WHERE c.session_id=r.id AND c.status IN ('pending','admitted'))::bigint AS pending,
 (SELECT count(*) FROM scenario_recorder_requests c WHERE c.session_id=r.id AND c.status='failed')::bigint AS failed
FROM scenario_recorder_sessions r JOIN scenario_test_action_policies p ON p.app_id=r.app_id
WHERE r.id=sqlc.arg(id)::text::uuid AND r.app_id=sqlc.arg(app_id)::text::uuid
 AND r.instance_id=sqlc.arg(instance_id)::text::uuid AND r.deployment_id=sqlc.arg(deployment_id)::text::uuid
 AND p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text AND p.sealed_at IS NULL;

-- name: ScenarioRecorderRunCounts :one
SELECT (SELECT count(*) FROM scenario_recorder_sessions r JOIN scenario_test_action_policies p ON p.app_id=r.app_id
 WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text)::bigint AS sessions,
 (SELECT count(*) FROM scenario_recorder_requests c JOIN scenario_recorder_sessions r ON r.id=c.session_id JOIN scenario_test_action_policies p ON p.app_id=r.app_id
 WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text)::bigint AS requests;

-- name: InsertScenarioRecorderRequest :exec
INSERT INTO scenario_recorder_requests(id,session_id) VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(session_id)::text::uuid);

-- name: SetScenarioRecorderStatus :exec
UPDATE scenario_recorder_sessions SET status=sqlc.arg(status)::text WHERE id=sqlc.arg(id)::text::uuid AND status='recording';

-- name: ScenarioRecorderRequestAvailable :one
SELECT EXISTS(SELECT 1 FROM scenario_recorder_requests c JOIN scenario_recorder_sessions r ON r.id=c.session_id
 JOIN scenario_test_action_policies p ON p.app_id=r.app_id
 WHERE c.id=nullif(sqlc.arg(id)::text,'')::uuid AND c.status='pending' AND r.status='recording'
 AND r.app_id=sqlc.arg(app_id)::text::uuid AND r.instance_id=sqlc.arg(instance_id)::text::uuid
 AND scenario_rehearsal_execution_active(r.app_id,r.instance_id,r.deployment_id)
 AND p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text AND p.sealed_at IS NULL) AS available;

-- name: ScenarioRecorderAttemptOwner :one
SELECT EXISTS(SELECT 1 FROM scenario_recorder_requests c JOIN scenario_recorder_sessions r ON r.id=c.session_id
 JOIN scenario_test_action_policies p ON p.app_id=r.app_id
 WHERE c.id=nullif(sqlc.arg(id)::text,'')::uuid AND c.attempt_id=sqlc.arg(attempt_id)::text::uuid AND c.status='admitted'
 AND r.app_id=sqlc.arg(app_id)::text::uuid AND r.instance_id=sqlc.arg(instance_id)::text::uuid
 AND p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text AND p.sealed_at IS NULL) AS owned;

-- name: AdmitScenarioRecorderRequest :execrows
UPDATE scenario_recorder_requests SET attempt_id=sqlc.arg(attempt_id)::text::uuid,status=sqlc.arg(status)::text
WHERE id=sqlc.arg(id)::text::uuid AND status='pending';

-- name: FinishScenarioRecorderRequest :execrows
UPDATE scenario_recorder_requests SET status=sqlc.arg(status)::text
WHERE id=sqlc.arg(id)::text::uuid AND attempt_id=sqlc.arg(attempt_id)::text::uuid AND status='admitted';

-- name: CloseScenarioRecorderAdmission :exec
UPDATE scenario_test_action_policies SET recorder_closing=true
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text AND simulation_plan IS NOT NULL AND sealed_at IS NULL;

-- name: ScenarioRecorderSnapshot :one
WITH members AS (SELECT app_id FROM scenario_test_action_policies
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text AND simulation_plan IS NOT NULL),
 sessions AS (SELECT r.* FROM scenario_recorder_sessions r JOIN members m ON m.app_id=r.app_id)
SELECT (SELECT count(*) FROM sessions)::bigint AS sessions,
 (SELECT count(*) FROM sessions WHERE status='recording')::bigint AS pending,
 ((SELECT count(*) FROM sessions WHERE status='failed')+
 (SELECT count(*) FROM members m WHERE NOT EXISTS(SELECT 1 FROM sessions r WHERE r.app_id=m.app_id)
 AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.app_id=m.app_id))+
 (SELECT count(*) FROM instances i JOIN members m ON m.app_id=i.app_id WHERE NOT EXISTS(SELECT 1 FROM sessions r WHERE r.instance_id=i.id AND r.app_id=i.app_id))+
 (SELECT count(*) FROM app_tasks t JOIN members m ON m.app_id=t.app_id WHERE t.command[1]='__gregale_rehearsal_command_v1__'
 AND t.status NOT IN ('queued','restoring','running') AND (t.status<>'succeeded' OR t.output_truncated OR NOT EXISTS
 (SELECT 1 FROM sessions r WHERE r.instance_id=t.id AND r.app_id=t.app_id AND r.status='closed' AND r.command_result=t.stdout_tail))))::bigint AS incomplete;

-- name: RetireScenarioCommandRecorder :execrows
UPDATE scenario_recorder_sessions SET status='closed',command_result=sqlc.arg(result)::text
WHERE id=sqlc.arg(id)::text::uuid AND status='recording'
 AND EXISTS(SELECT 1 FROM scenario_command_networks n WHERE n.task_id=scenario_recorder_sessions.instance_id AND NOT n.active)
 AND NOT EXISTS(SELECT 1 FROM scenario_recorder_requests WHERE session_id=scenario_recorder_sessions.id AND status<>'completed');

-- name: PendingScenarioCommands :one
SELECT count(*) FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text
 AND t.command[1]='__gregale_rehearsal_command_v1__' AND t.status IN ('queued','restoring','running');

-- name: RecordScenarioCommandNetwork :execrows
INSERT INTO scenario_command_networks(task_id,node_id,host_ip,lease_token)
SELECT t.id,sqlc.arg(node_id)::text::uuid,sqlc.arg(host_ip)::text::inet,t.lease_token FROM app_tasks t
WHERE t.id=sqlc.arg(task_id)::text::uuid AND t.lease_token=sqlc.arg(lease_token)::text::uuid
 AND t.status='restoring' AND t.lease_expires_at>clock_timestamp() AND t.cancel_requested_at IS NULL;

-- name: FenceScenarioCommandNetwork :exec
UPDATE scenario_command_networks SET active=false WHERE task_id=sqlc.arg(task_id)::text::uuid AND active;

-- name: ScenarioCommandCallersByHostIP :many
SELECT t.id::text AS instance_id,t.app_id::text AS app_id,t.deployment_id::text AS deployment_id
FROM scenario_command_networks c JOIN app_tasks t ON t.id=c.task_id JOIN compute_nodes n ON n.id=c.node_id
JOIN scenario_recorder_sessions r ON r.instance_id=t.id AND r.app_id=t.app_id
JOIN scenario_test_action_policies p ON p.app_id=t.app_id JOIN apps a ON a.id=t.app_id
WHERE c.active AND c.host_ip=sqlc.arg(host_ip)::text::inet AND (sqlc.arg(node_name)::text='' OR n.name=sqlc.arg(node_name)::text)
 AND t.lease_token=c.lease_token AND t.lease_expires_at>clock_timestamp() AND t.cancel_requested_at IS NULL
 AND t.status='running' AND r.status='recording' AND p.sealed_at IS NULL AND NOT p.recorder_closing
 AND a.status<>'deleted' AND scenario_rehearsal_execution_active(t.app_id,t.id,t.deployment_id)
LIMIT 2;

-- name: RevokeScenarioCommandNetworksForNode :exec
UPDATE scenario_command_networks c SET active=false WHERE active
 AND (c.node_id=nullif(sqlc.arg(node_id)::text,'')::uuid OR (sqlc.arg(node_id)::text='' AND EXISTS
 (SELECT 1 FROM compute_nodes n WHERE n.id=c.node_id AND n.name=sqlc.arg(local_name)::text)));
