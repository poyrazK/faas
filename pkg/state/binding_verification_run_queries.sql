-- name: InsertBindingVerificationRun :exec
INSERT INTO binding_verification_runs(id,account_id,app_id,deployment_id,scope,status,snapshot,created_at,expires_at)
VALUES(sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(deployment_id),sqlc.arg(scope),'pending',sqlc.arg(snapshot),sqlc.arg(created_at),sqlc.arg(expires_at));
-- name: ReadBindingVerificationRun :one
SELECT * FROM binding_verification_runs WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id);
-- name: LockBindingVerificationRun :one
SELECT * FROM binding_verification_runs WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) FOR UPDATE;
-- name: CountActiveBindingVerificationRuns :one
SELECT count(*)::bigint FROM binding_verification_runs WHERE app_id=sqlc.arg(app_id) AND status='pending' AND expires_at>clock_timestamp();
-- name: RecordBindingVerificationRunStep :exec
INSERT INTO binding_verification_run_steps(run_id,step_index,resource_id,resource_kind) VALUES(sqlc.arg(run_id),sqlc.arg(step_index),sqlc.arg(resource_id),sqlc.arg(resource_kind));
-- name: ReadBindingVerificationRunSteps :many
SELECT * FROM binding_verification_run_steps WHERE run_id=sqlc.arg(run_id) ORDER BY step_index;
-- name: SetBindingVerificationRunStatus :exec
UPDATE binding_verification_runs SET status=sqlc.arg(status) WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id);
-- name: ReadBindingVerificationRunAuthority :one
SELECT to_jsonb(d)::jsonb AS artifact,
 c.changed_at, to_jsonb(p)::jsonb AS policy
FROM apps a JOIN deployments d ON d.app_id=a.id
JOIN app_runtime_config_changes c ON c.app_id=a.id
JOIN app_binding_release_policies p ON p.app_id=a.id AND p.scope=sqlc.arg(scope)
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
AND d.id=sqlc.arg(deployment_id) AND d.status='live' AND d.deleted_at IS NULL
AND d.rootfs_key<>''
FOR SHARE OF d,c,p;
-- name: SetVerificationRunTaskDeadline :exec
UPDATE app_tasks SET start_deadline_at=sqlc.arg(expires_at) WHERE id=sqlc.arg(id);
-- name: DeleteExpiredBindingVerificationRunReceipts :exec
DELETE FROM binding_verification_runs WHERE app_id=sqlc.arg(app_id) AND graph_run_id IS NULL AND expires_at < clock_timestamp()-interval '1 day';
-- name: CountUnfinishedVerificationResources :one
SELECT (
 (SELECT count(*) FROM app_tasks t WHERE t.account_id=sqlc.arg(account_id) AND t.id=ANY(sqlc.arg(task_ids)::uuid[]) AND t.status NOT IN ('succeeded','failed','timed_out','cancelled'))
 + (SELECT count(*) FROM invocations i WHERE i.account_id=sqlc.arg(account_id) AND i.id=ANY(sqlc.arg(queue_ids)::uuid[]) AND i.state NOT IN ('completed','failed','cancelled','expired','superseded','dead_letter'))
)::bigint AS active_resources;
-- name: ListBindingVerificationRunSummaries :many
SELECT r.id, a.slug AS app, r.deployment_id, r.scope, r.graph_run_id,
 CASE WHEN r.status <> 'cancelled' AND r.expires_at <= sqlc.arg(observed_at) THEN 'expired' ELSE r.status END::text AS status,
 r.created_at, r.expires_at
FROM binding_verification_runs r JOIN apps a ON a.id=r.app_id AND a.account_id=r.account_id
WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id)
AND (sqlc.arg(include_members)::boolean OR r.graph_run_id IS NULL)
AND (sqlc.arg(deployment)::text='' OR r.deployment_id::text=sqlc.arg(deployment))
AND (sqlc.arg(scope)::text='' OR r.scope=sqlc.arg(scope))
AND (sqlc.arg(status_filter)::text='' OR CASE WHEN r.status <> 'cancelled' AND r.expires_at <= sqlc.arg(observed_at) THEN 'expired' ELSE r.status END=sqlc.arg(status_filter))
AND r.created_at <= sqlc.arg(observed_at)
AND (NOT sqlc.arg(has_cursor)::boolean OR (r.created_at,r.id)<(sqlc.arg(before_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY r.created_at DESC,r.id DESC LIMIT sqlc.arg(page_limit);
