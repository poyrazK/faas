-- name: WorkAdmissionEnsureLane :exec
INSERT INTO invocation_work_lanes (app_id, policy_name, key_digest)
VALUES (sqlc.arg(app_id)::uuid, sqlc.arg(policy_name)::text, sqlc.arg(key_digest)::bytea)
ON CONFLICT DO NOTHING;

-- name: WorkAdmissionInvocation :one
SELECT * FROM invocations WHERE id=sqlc.arg(id)::uuid;

-- name: WorkAdmissionSupersede :exec
UPDATE invocations SET state='superseded', outcome='superseded', completed_at=clock_timestamp(),
    last_error='superseded by newer work'
WHERE app_id=sqlc.arg(app_id)::uuid AND work_policy_name=sqlc.arg(policy_name)::text
    AND work_key_digest=sqlc.arg(key_digest)::bytea AND state='pending';

-- name: WorkAdmissionSupersedeBroker :exec
UPDATE trigger_records tr SET state='superseded', last_error='superseded by newer work', claim_expires_at=NULL
FROM triggers t WHERE t.id=tr.trigger_id AND t.app_id=sqlc.arg(app_id)::uuid
    AND tr.work_policy_name=sqlc.arg(policy_name)::text AND tr.work_key_digest=sqlc.arg(key_digest)::bytea
    AND tr.state IN ('pending','retry');

-- name: WorkAdmissionCancellation :one
SELECT * FROM invocation_work_cancellations WHERE id=sqlc.arg(id)::uuid;

-- name: WorkAdmissionInsertCancellation :one
INSERT INTO invocation_work_cancellations (id,app_id,policy_name,key_digest)
VALUES (sqlc.arg(id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(policy_name)::text,sqlc.arg(key_digest)::bytea)
ON CONFLICT (id) DO NOTHING RETURNING id;

-- name: WorkAdmissionCancel :execrows
UPDATE invocations SET state='cancelled',completed_at=clock_timestamp()
WHERE app_id=sqlc.arg(app_id)::uuid AND work_policy_name=sqlc.arg(policy_name)::text
    AND work_key_digest=sqlc.arg(key_digest)::bytea AND state='pending';

-- name: WorkAdmissionCancelBroker :execrows
UPDATE trigger_records tr SET state='cancelled',last_error='cancelled by work policy',claim_expires_at=NULL
FROM triggers t WHERE t.id=tr.trigger_id AND t.app_id=sqlc.arg(app_id)::uuid
    AND tr.work_policy_name=sqlc.arg(policy_name)::text AND tr.work_key_digest=sqlc.arg(key_digest)::bytea
    AND tr.state IN ('pending','retry');

-- name: WorkAdmissionFinishCancellation :one
UPDATE invocation_work_cancellations SET cancelled_count=sqlc.arg(cancelled_count)::bigint
WHERE id=sqlc.arg(id)::uuid RETURNING *;
