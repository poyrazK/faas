-- name: IssueListTeams :many
SELECT t.* FROM app_issue_teams t JOIN apps a ON a.id=t.app_id WHERE t.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid ORDER BY t.name,t.team_key;
-- name: IssueGetTeam :one
SELECT t.* FROM app_issue_teams t JOIN apps a ON a.id=t.app_id WHERE t.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND t.team_key=sqlc.arg(team_key)::text;
-- name: IssueCountTeams :one
SELECT count(*) FROM app_issue_teams WHERE app_id=$1;
-- name: IssuePutTeam :one
INSERT INTO app_issue_teams(app_id,team_key,name,created_at,updated_at,updated_by_account_id)
VALUES(sqlc.arg(app_id),sqlc.arg(team_key),sqlc.arg(name),sqlc.arg(now),sqlc.arg(now),sqlc.arg(actor_account_id))
ON CONFLICT(app_id,team_key) DO UPDATE SET name=excluded.name,updated_at=excluded.updated_at,updated_by_account_id=excluded.updated_by_account_id RETURNING *;
-- name: IssueTeamMembers :many
SELECT account_id FROM app_issue_team_members WHERE team_id=$1 ORDER BY account_id;
-- name: IssueDeleteTeamMembers :exec
DELETE FROM app_issue_team_members WHERE team_id=$1;
-- name: IssueAddTeamMember :exec
INSERT INTO app_issue_team_members(team_id,account_id) VALUES($1,$2);
-- name: IssueDeleteTeam :execrows
DELETE FROM app_issue_teams WHERE app_id=sqlc.arg(app_id) AND team_key=sqlc.arg(team_key);
-- name: IssueTeamReferenced :one
SELECT EXISTS(SELECT 1 FROM app_issues i WHERE i.assignee_team_id=sqlc.arg(team_id) UNION ALL SELECT 1 FROM app_issue_ownership_rules r WHERE r.assignee_team_id=sqlc.arg(team_id));
-- name: IssueTeamExists :one
SELECT EXISTS(SELECT 1 FROM app_issue_teams WHERE app_id=sqlc.arg(app_id) AND id=sqlc.arg(team_id));
-- name: IssueTeamAllowed :one
SELECT EXISTS(SELECT 1 FROM app_issue_teams t JOIN apps a ON a.id=t.app_id JOIN app_issue_team_members tm ON tm.team_id=t.id
WHERE t.app_id=sqlc.arg(app_id) AND t.id=sqlc.arg(team_id) AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)));
