-- name: IssueTrackerLinearOwner :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id) AND deleted_at IS NULL AND status<>'deleted' FOR UPDATE;
-- name: IssueTrackerLinearGet :one
SELECT * FROM issue_tracker_linear_connections WHERE app_id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id);
-- name: IssueTrackerLinearPut :one
INSERT INTO issue_tracker_linear_connections(app_id,account_id,revision,connected,workspace_id,team_id,viewer_id,sealed_credential)
VALUES(sqlc.arg(app_id),sqlc.arg(account_id),1,sqlc.arg(connected),sqlc.narg(workspace_id),sqlc.narg(team_id),sqlc.narg(viewer_id),sqlc.narg(sealed_credential))
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,revision=issue_tracker_linear_connections.revision+1,connected=excluded.connected,
workspace_id=excluded.workspace_id,team_id=excluded.team_id,viewer_id=excluded.viewer_id,sealed_credential=excluded.sealed_credential,updated_at=now()
RETURNING *;
