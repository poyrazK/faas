-- name: IssueSavedViewOwner :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: IssueListSavedViews :many
SELECT v.* FROM app_issue_saved_views v JOIN apps a ON a.id=v.app_id WHERE v.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid ORDER BY v.name,v.view_key;

-- name: IssueGetSavedView :one
SELECT v.* FROM app_issue_saved_views v JOIN apps a ON a.id=v.app_id WHERE v.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND v.view_key=sqlc.arg(view_key)::text;

-- name: IssueCountSavedViews :one
SELECT count(*) FROM app_issue_saved_views WHERE app_id=$1;

-- name: IssuePutSavedView :one
INSERT INTO app_issue_saved_views(app_id,view_key,name,filters,created_at,updated_at,updated_by_account_id)
VALUES(sqlc.arg(app_id),sqlc.arg(view_key),sqlc.arg(name),sqlc.arg(filters),sqlc.arg(now),sqlc.arg(now),sqlc.arg(actor_account_id))
ON CONFLICT(app_id,view_key) DO UPDATE SET name=excluded.name,filters=excluded.filters,updated_at=excluded.updated_at,updated_by_account_id=excluded.updated_by_account_id
RETURNING *;

-- name: IssueDeleteSavedView :execrows
DELETE FROM app_issue_saved_views WHERE app_id=sqlc.arg(app_id)::uuid AND view_key=sqlc.arg(view_key)::text;
