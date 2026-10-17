-- name: IssueSlackInstallation :one
SELECT * FROM issue_slack_installations WHERE app_id=$1 AND account_id=$2;
-- name: IssueSlackOAuthFlowPut :exec
INSERT INTO issue_slack_oauth_flows(app_id,account_id,state_hash,expected_revision,phase,sealed,expires_at)
VALUES($1,$2,$3,$4,'authorizing',$5,$6)
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,state_hash=excluded.state_hash,expected_revision=excluded.expected_revision,phase='authorizing',sealed=excluded.sealed,expires_at=excluded.expires_at;
-- name: IssueSlackOAuthFlow :one
SELECT f.* FROM issue_slack_oauth_flows f JOIN apps a ON a.id=f.app_id AND a.account_id=f.account_id
WHERE f.state_hash=$1 AND f.account_id=$2 AND f.expires_at>clock_timestamp() AND a.deleted_at IS NULL;
-- name: IssueSlackOAuthFlowClaim :one
UPDATE issue_slack_oauth_flows f SET phase='exchanging'
WHERE f.state_hash=$1 AND f.account_id=$2 AND f.phase='authorizing' AND f.expires_at>clock_timestamp()
AND EXISTS(SELECT 1 FROM apps a JOIN accounts owner ON owner.id=a.account_id
 WHERE a.id=f.app_id AND a.account_id=f.account_id AND a.deleted_at IS NULL AND owner.status='active' AND owner.abuse_hold_at IS NULL)
RETURNING f.*;
-- name: IssueSlackOAuthFlowSave :one
UPDATE issue_slack_oauth_flows SET phase='choosing',sealed=$3
WHERE state_hash=$1 AND account_id=$2 AND phase='exchanging' AND expires_at>clock_timestamp() RETURNING *;
-- name: IssueSlackOAuthFlowDelete :exec
DELETE FROM issue_slack_oauth_flows WHERE app_id=$1;
-- name: IssueSlackInstallationPut :one
INSERT INTO issue_slack_installations(app_id,account_id,revision,status,workspace_id,workspace_name,slack_app_id,bot_user_id,token_sealed,installed_at)
VALUES($1,$2,$3,'connected',$4,$5,$6,$7,$8,clock_timestamp())
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,revision=excluded.revision,status='connected',workspace_id=excluded.workspace_id,workspace_name=excluded.workspace_name,slack_app_id=excluded.slack_app_id,bot_user_id=excluded.bot_user_id,token_sealed=excluded.token_sealed,installed_at=excluded.installed_at RETURNING *;
-- name: IssueSlackInstallationDisable :exec
UPDATE issue_slack_installations SET status=$2,revision=revision+1,token_sealed=''::bytea WHERE app_id=$1;
-- name: IssueSlackOAuthDestinationsDisable :exec
UPDATE issue_team_slack_destinations SET enabled=false,token_sealed=''::bytea,revision=revision+1,claim_id=NULL,claim_until=NULL
WHERE app_id=$1 AND (oauth_revision IS NOT NULL OR sqlc.arg(all_destinations)::boolean);
-- name: IssueSlackOAuthDeliveriesCancel :exec
UPDATE issue_team_slack_deliveries d SET status='canceled',last_error='destination_unavailable',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
FROM issue_team_slack_destinations dst WHERE dst.id=d.destination_id AND dst.app_id=$1 AND dst.revision<>d.destination_revision AND d.status IN ('pending','failed','in_flight');
-- name: IssueSlackOAuthInteractionsDisable :exec
UPDATE issue_slack_interaction_policies SET enabled=false,revision=revision+1 WHERE app_id=$1;
-- name: IssueSlackOAuthDestinationBind :exec
UPDATE issue_team_slack_destinations SET oauth_revision=$2 WHERE id=$1;
-- name: IssueSlackOAuthAffectedApps :many
SELECT i.app_id,i.account_id FROM issue_slack_installations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id AND a.deleted_at IS NULL
WHERE i.status='connected' AND i.slack_app_id=$1 AND i.workspace_id=$2 AND i.installed_at<=sqlc.arg(event_time)
AND (sqlc.arg(bot_user_id)::text='' OR i.bot_user_id=sqlc.arg(bot_user_id)) ORDER BY i.app_id;
-- name: IssueSlackOAuthActionApp :one
SELECT i.app_id FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN issue_slack_installations i ON i.app_id=dst.app_id AND i.status='connected' AND i.revision=dst.oauth_revision
WHERE d.id=$1 AND i.slack_app_id=$2 AND i.workspace_id=$3;
-- name: IssueSlackOAuthOwnerReady :one
SELECT EXISTS(SELECT 1 FROM apps a JOIN accounts owner ON owner.id=a.account_id WHERE a.id=$1 AND a.account_id=$2
AND a.deleted_at IS NULL AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]));
-- name: IssueSlackOAuthExpiredCleanup :exec
WITH expired AS (SELECT app_id FROM issue_slack_oauth_flows WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED)
DELETE FROM issue_slack_oauth_flows f USING expired e WHERE f.app_id=e.app_id;
