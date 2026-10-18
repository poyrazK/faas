-- name: Enrollment :one
SELECT managed_work_plan,coalesce(workload_name,'')::text AS workload_name FROM scenario_test_action_policies
WHERE app_id=sqlc.arg(app_id)::text::uuid;

-- name: Checkpoint :one
SELECT f.id::text AS resource_id,f.owner_id::text AS owner_id,coalesce(f.backend_sha256,'')::text AS backend_sha256,
 i.deployment_id::text AS deployment_id,v.id::text AS invocation_id,r.id::text AS recorder_session_id
FROM scenario_test_action_policies p
JOIN scenario_postgres_fixtures f ON f.account_id=p.account_id AND f.run_id=p.run_id
JOIN instances i ON i.app_id=p.app_id AND i.id=sqlc.arg(instance_id)::text::uuid
JOIN invocations v ON v.app_id=p.app_id AND nullif(v.instance_id,'')::uuid=i.id
JOIN scenario_recorder_sessions r ON r.app_id=p.app_id AND r.instance_id=i.id AND r.deployment_id=i.deployment_id
JOIN scenario_recorder_requests c ON c.session_id=r.id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid AND p.sealed_at IS NULL AND NOT p.recorder_closing AND NOT p.managed_work_failed
 AND f.status='ready' AND f.expires_at>clock_timestamp() AND i.state='running' AND r.status='recording'
 AND v.source='async_invoke' AND v.state='dispatching' AND v.attempts=1 AND v.replay_generation=0 AND v.lease_expires_at>clock_timestamp()
 AND c.attempt_id=sqlc.arg(attempt_id)::text::uuid AND c.id=sqlc.arg(request_id)::text::uuid AND c.status='admitted'
 AND (SELECT count(*) FROM invocations x JOIN scenario_test_action_policies w ON w.app_id=x.app_id WHERE w.account_id=p.account_id AND w.run_id=p.run_id)=1
 AND (SELECT count(*) FROM deployments d WHERE d.app_id=p.app_id)=1;

-- name: Insert :exec
INSERT INTO scenario_postgres_faults(id,resource_id,account_id,run_id,app_id,owner_id,phase,receipt)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(resource_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text,
 sqlc.arg(app_id)::text::uuid,sqlc.arg(owner_id)::text::uuid,'requested',sqlc.arg(receipt)::jsonb);

-- name: Get :one
SELECT receipt FROM scenario_postgres_faults WHERE id=sqlc.arg(id)::text::uuid;
-- name: ForRun :one
SELECT receipt FROM scenario_postgres_faults WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text;
-- name: Pending :many
SELECT receipt FROM scenario_postgres_faults WHERE phase<>'recovered'
 AND NOT (phase='failed' AND receipt->>'recovered'='true') ORDER BY receipt->>'created_at',id LIMIT 256;
-- name: Transition :execrows
UPDATE scenario_postgres_faults SET phase=sqlc.arg(phase)::text,receipt=sqlc.arg(receipt)::jsonb
WHERE id=sqlc.arg(id)::text::uuid AND owner_id=sqlc.arg(owner_id)::text::uuid AND phase=sqlc.arg(expected)::text;
