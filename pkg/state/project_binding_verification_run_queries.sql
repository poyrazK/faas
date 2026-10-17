-- name: InsertProjectBindingVerificationRun :exec
INSERT INTO project_binding_verification_runs(id,account_id,project_id,environment_slug,status,snapshot,created_at,expires_at)
VALUES(sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(project_id),sqlc.arg(environment),'pending',sqlc.arg(snapshot),sqlc.arg(created_at),sqlc.arg(expires_at));
-- name: ReadProjectBindingVerificationRun :one
SELECT * FROM project_binding_verification_runs WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND project_id=sqlc.arg(project_id) AND environment_slug=sqlc.arg(environment);
-- name: LockProjectBindingVerificationRun :one
SELECT * FROM project_binding_verification_runs WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) FOR UPDATE;
-- name: ShareProjectBindingVerificationRun :one
SELECT * FROM project_binding_verification_runs WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) FOR SHARE;
-- name: SetProjectBindingVerificationRunStatus :exec
UPDATE project_binding_verification_runs SET status=sqlc.arg(status) WHERE id=sqlc.arg(id);
-- name: LinkProjectBindingVerificationMember :execrows
UPDATE binding_verification_runs SET graph_run_id=sqlc.arg(graph_run_id) WHERE id=sqlc.arg(id) AND graph_run_id IS NULL;
-- name: CountActiveProjectBindingVerificationRuns :one
SELECT count(*)::bigint FROM project_binding_verification_runs WHERE project_id=sqlc.arg(project_id) AND environment_slug=sqlc.arg(environment) AND status='pending' AND expires_at>clock_timestamp();
-- name: PruneProjectBindingVerificationRuns :exec
DELETE FROM binding_verification_runs WHERE graph_run_id IN (SELECT id FROM project_binding_verification_runs WHERE project_id=sqlc.arg(project_id) AND expires_at<clock_timestamp()-interval '1 day');
-- name: PruneProjectBindingVerificationRunReceipts :exec
DELETE FROM project_binding_verification_runs WHERE project_id=sqlc.arg(project_id) AND expires_at<clock_timestamp()-interval '1 day';
-- name: ReadProjectBindingVerificationRunForBudget :one
SELECT * FROM project_binding_verification_runs WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id);
-- name: ListProjectBindingVerificationRunSummaries :many
SELECT r.id, p.slug AS project, r.environment_slug AS environment,
 CASE WHEN r.status <> 'cancelled' AND r.expires_at <= sqlc.arg(observed_at) THEN 'expired' ELSE r.status END::text AS status,
 r.created_at, r.expires_at
FROM project_binding_verification_runs r JOIN projects p ON p.id=r.project_id AND p.account_id=r.account_id
WHERE r.account_id=sqlc.arg(account_id) AND r.project_id=sqlc.arg(project_id) AND r.environment_slug=sqlc.arg(environment)
AND (sqlc.arg(deployment)::text='' OR EXISTS (SELECT 1 FROM jsonb_each_text(r.snapshot->'Report'->'manifest'->'deployments') d WHERE d.value=sqlc.arg(deployment)))
AND (sqlc.arg(status_filter)::text='' OR CASE WHEN r.status <> 'cancelled' AND r.expires_at <= sqlc.arg(observed_at) THEN 'expired' ELSE r.status END=sqlc.arg(status_filter))
AND r.created_at <= sqlc.arg(observed_at)
AND (NOT sqlc.arg(has_cursor)::boolean OR (r.created_at,r.id)<(sqlc.arg(before_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY r.created_at DESC,r.id DESC LIMIT sqlc.arg(page_limit);
