-- name: IssueListTrackerLinks :many
SELECT l.* FROM issue_tracker_links l JOIN issue_group_assignments a ON a.original_issue_id=l.issue_id
WHERE a.canonical_issue_id=sqlc.arg(issue_id)
AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (l.created_at,l.id)<(sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY l.created_at DESC,l.id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueCountTrackerLinks :one
SELECT count(*) FROM issue_tracker_links l JOIN issue_group_assignments a ON a.original_issue_id=l.issue_id WHERE a.canonical_issue_id=$1;
-- name: IssueFindTrackerLink :one
SELECT l.* FROM issue_tracker_links l JOIN issue_group_assignments a ON a.original_issue_id=l.issue_id
WHERE a.canonical_issue_id=sqlc.arg(issue_id) AND l.provider=sqlc.arg(provider) AND l.external_key=sqlc.arg(external_key)
ORDER BY l.created_at DESC,l.id DESC LIMIT 1;
-- name: IssueGetTrackerLink :one
SELECT l.* FROM issue_tracker_links l JOIN issue_group_assignments a ON a.original_issue_id=l.issue_id
WHERE a.canonical_issue_id=sqlc.arg(issue_id) AND l.id=sqlc.arg(id);
-- name: IssueCreateTrackerLink :one
INSERT INTO issue_tracker_links(issue_id,provider,external_key,url,title,created_at,updated_at,created_by_account_id,updated_by_account_id)
VALUES(sqlc.arg(issue_id),sqlc.arg(provider),sqlc.arg(external_key),sqlc.arg(url),sqlc.arg(title),sqlc.arg(now),sqlc.arg(now),sqlc.arg(actor),sqlc.arg(actor)) RETURNING *;
-- name: IssueUpdateTrackerLink :one
UPDATE issue_tracker_links SET title=sqlc.arg(title),updated_at=sqlc.arg(now),updated_by_account_id=sqlc.arg(actor) WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueDeleteTrackerLink :exec
DELETE FROM issue_tracker_links WHERE id=$1;
