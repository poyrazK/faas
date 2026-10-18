-- name: ScenarioSuspendResume :one
SELECT r.id::text AS id,r.account_id::text AS account_id,r.app_id::text AS app_id,r.run_id,p.workload_name,
 r.deployment_id::text AS deployment_id,r.instance_id::text AS instance_id,r.recorder_session_id::text AS recorder_session_id,
 coalesce(r.invocation_id::text,'')::text AS invocation_id,coalesce(r.invocation_attempt,0)::integer AS invocation_attempt,coalesce(r.checkpoint_attempt_id::text,'')::text AS checkpoint_attempt_id,
 r.state,coalesce(r.method,'')::text AS method,r.pause_ms,r.resume_ms
FROM scenario_suspend_resumes r JOIN scenario_test_action_policies p ON p.app_id=r.app_id WHERE r.id=sqlc.arg(id)::text::uuid;

-- name: ScenarioSuspendResumesForRun :many
SELECT r.id::text AS id FROM scenario_suspend_resumes r
WHERE r.account_id=sqlc.arg(account_id)::text::uuid AND r.run_id=sqlc.arg(run_id)::text ORDER BY r.app_id;

-- name: PendingScenarioSuspendResumes :many
SELECT r.id::text FROM scenario_suspend_resumes r JOIN apps a ON a.id=r.app_id
JOIN scenario_test_action_policies p ON p.app_id=r.app_id
WHERE r.state NOT IN ('waiting','complete','failed') AND a.status<>'deleted' AND a.preview_pr_state='open'
 AND a.preview_expires_at>clock_timestamp() AND p.sealed_at IS NULL AND NOT p.recorder_closing AND NOT p.simulation_failed
ORDER BY r.created_at,r.id LIMIT 16;

-- name: InsertScenarioSuspendResume :exec
INSERT INTO scenario_suspend_resumes(id,account_id,app_id,run_id,deployment_id,instance_id,recorder_session_id,state)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(run_id)::text,
 sqlc.arg(deployment_id)::text::uuid,sqlc.arg(instance_id)::text::uuid,sqlc.arg(session_id)::text::uuid,'armed');

-- name: TransitionScenarioSuspendResume :execrows
UPDATE scenario_suspend_resumes SET state=sqlc.arg(state)::text,
 paused_at=CASE WHEN sqlc.arg(state)::text='paused' THEN clock_timestamp() ELSE paused_at END,
 pause_ms=CASE WHEN sqlc.arg(state)::text='resuming' THEN sqlc.arg(pause_ms)::bigint ELSE pause_ms END,
 resume_ms=CASE WHEN sqlc.arg(state)::text='resumed' THEN sqlc.arg(resume_ms)::bigint ELSE resume_ms END,
 method=CASE WHEN sqlc.arg(state)::text='resumed' THEN 'in_place' ELSE method END
WHERE id=sqlc.arg(id)::text::uuid AND state=sqlc.arg(expected)::text;

-- name: FailScenarioSuspendResume :exec
UPDATE scenario_suspend_resumes SET state='failed' WHERE id=sqlc.arg(id)::text::uuid AND state NOT IN ('complete','failed');

-- name: ScenarioResumingCallersByHostIP :many
SELECT i.app_id::text AS app_id,i.deployment_id::text AS deployment_id
FROM instances i JOIN compute_nodes n ON n.id=i.node_id
JOIN apps a ON a.id=i.app_id AND a.status='active' AND a.preview_pr_state='open' AND a.preview_expires_at>clock_timestamp()
JOIN scenario_suspend_resumes c ON c.instance_id=i.id AND c.app_id=i.app_id AND c.deployment_id=i.deployment_id
JOIN scenario_recorder_sessions r ON r.id=c.recorder_session_id AND r.instance_id=i.id AND r.status='recording'
JOIN scenario_test_action_policies p ON p.app_id=i.app_id
WHERE i.state='frozen' AND c.state IN ('resuming','resumed')
 AND i.host_ip=sqlc.arg(host_ip)::text::inet AND (sqlc.arg(node_name)::text='' OR n.name=sqlc.arg(node_name)::text)
 AND p.account_id=c.account_id AND p.run_id=c.run_id AND p.sealed_at IS NULL AND NOT p.recorder_closing AND NOT p.simulation_failed
 AND EXISTS(SELECT 1 FROM scenario_test_members m WHERE m.app_id=i.app_id AND m.account_id=p.account_id AND m.run_id=p.run_id)
 AND scenario_rehearsal_execution_active(i.app_id,i.id,i.deployment_id)
LIMIT 2;

-- name: ScenarioSuspendCommandsPending :one
SELECT EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
 WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text
 AND t.status IN ('queued','restoring','running')) AS pending;

-- name: ScenarioSuspendCheckpoint :one
SELECT p.account_id::text AS account_id,i.deployment_id::text AS deployment_id,v.id::text AS invocation_id,r.id::text AS recorder_session_id
FROM scenario_test_action_policies p JOIN instances i ON i.app_id=p.app_id
JOIN invocations v ON v.app_id=p.app_id AND nullif(v.instance_id,'')::uuid=i.id
JOIN scenario_recorder_sessions r ON r.instance_id=i.id AND r.app_id=p.app_id AND r.deployment_id=i.deployment_id
JOIN scenario_recorder_requests c ON c.session_id=r.id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid AND i.id=sqlc.arg(instance_id)::text::uuid AND i.state='running'
 AND c.attempt_id=sqlc.arg(attempt_id)::text::uuid AND c.status='admitted' AND r.status='recording'
 AND p.sealed_at IS NULL AND NOT p.recorder_closing AND NOT p.managed_work_failed
 AND p.suspend_resume_spec->>'checkpoint'='managed_job'
 AND p.suspend_resume_spec->'workloads'->>0=p.workload_name
 AND p.suspend_resume_spec->>'worker_checkpoint'=sqlc.arg(checkpoint)::text
 AND scenario_rehearsal_execution_active(p.app_id,i.id,i.deployment_id)
 AND v.source='async_invoke' AND v.state='dispatching' AND v.attempts=1 AND v.replay_generation=0 AND v.lease_expires_at>clock_timestamp()
 AND (SELECT count(*) FROM invocations x JOIN scenario_test_action_policies w ON w.app_id=x.app_id WHERE w.account_id=p.account_id AND w.run_id=p.run_id)=1
 AND (SELECT count(*) FROM deployments d WHERE d.app_id=p.app_id)=1;

-- name: InsertScenarioSuspendCheckpoint :exec
INSERT INTO scenario_suspend_resumes(id,account_id,app_id,run_id,deployment_id,instance_id,recorder_session_id,state,invocation_id,invocation_attempt,checkpoint_attempt_id)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(run_id)::text,
 sqlc.arg(deployment_id)::text::uuid,sqlc.arg(instance_id)::text::uuid,sqlc.arg(session_id)::text::uuid,'waiting',sqlc.arg(invocation_id)::text::uuid,1,sqlc.arg(attempt_id)::text::uuid);
