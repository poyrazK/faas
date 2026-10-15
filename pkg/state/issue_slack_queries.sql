-- name: IssueSlackDestinationGet :one
SELECT d.* FROM issue_team_slack_destinations d JOIN apps a ON a.id=d.app_id AND a.account_id=d.account_id
WHERE d.app_id=$1 AND d.account_id=$2 AND d.team_id=$3;
-- name: IssueSlackDestinationList :many
SELECT d.id,d.team_id,d.channel_id,d.enabled,d.revision,t.name AS team_name FROM issue_team_slack_destinations d
JOIN apps a ON a.id=d.app_id AND a.account_id=d.account_id JOIN app_issue_teams t ON t.id=d.team_id AND t.app_id=a.id
WHERE a.id=$1 AND a.account_id=$2 ORDER BY t.name,d.id;
-- name: IssueSlackDestinationPut :one
INSERT INTO issue_team_slack_destinations(id,app_id,account_id,team_id,channel_id,token_sealed,enabled) VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(app_id,team_id) DO UPDATE SET account_id=excluded.account_id,channel_id=excluded.channel_id,token_sealed=excluded.token_sealed,enabled=excluded.enabled,revision=issue_team_slack_destinations.revision+1,claim_id=NULL,claim_until=NULL,next_send_at=clock_timestamp(),updated_at=clock_timestamp() RETURNING *;
-- name: IssueSlackCancelPrevious :exec
UPDATE issue_team_slack_deliveries SET status='canceled',last_error='destination_unavailable',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
WHERE destination_id=$1 AND destination_revision<>$2 AND status IN ('pending','failed','in_flight');
-- name: IssueSlackEnqueue :exec
INSERT INTO issue_team_slack_deliveries(id,destination_id,destination_revision,notification_id,incident_id,channel_id)
SELECT sqlc.arg(delivery_id),d.id,d.revision,n.id,n.incident_id,d.channel_id FROM issue_tracker_sync_notifications n
JOIN issue_team_slack_destinations d ON d.app_id=n.app_id AND d.account_id=n.account_id AND d.team_id=n.team_id AND d.enabled
WHERE n.id=sqlc.arg(notification_id) ON CONFLICT(destination_id,destination_revision,notification_id) DO NOTHING;
-- name: IssueSlackCandidateApp :one
SELECT a.id FROM apps a JOIN issue_team_slack_destinations dst ON dst.app_id=a.id
JOIN issue_team_slack_deliveries d ON d.destination_id=dst.id
WHERE d.status IN ('pending','failed','in_flight') AND LEAST(GREATEST(d.next_attempt_at,dst.next_send_at),d.expires_at)<=clock_timestamp()
AND (d.claim_until IS NULL OR d.claim_until<=clock_timestamp()) AND (dst.claim_until IS NULL OR dst.claim_until<=clock_timestamp())
AND NOT EXISTS(SELECT 1 FROM issue_team_slack_deliveries prev WHERE prev.destination_id=d.destination_id AND prev.destination_revision=d.destination_revision AND prev.incident_id=d.incident_id AND prev.status IN ('pending','failed','in_flight') AND prev.delivery_seq<d.delivery_seq)
ORDER BY d.delivery_seq LIMIT 1 FOR UPDATE OF a SKIP LOCKED;
-- name: IssueSlackCandidateDelivery :one
SELECT d.*,dst.app_id,dst.account_id,dst.team_id,dst.token_sealed,a.slug AS app_slug,n.issue_id,n.link_id,n.kind,n.reason,
 COALESCE(a.account_id=dst.account_id AND a.account_id=n.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
 AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
 AND dst.enabled AND dst.revision=d.destination_revision AND p.enabled AND n.team_id=dst.team_id
 AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
 AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL))),false)::boolean AS eligible
FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id JOIN accounts owner ON owner.id=a.account_id
JOIN issue_tracker_sync_notifications n ON n.id=d.notification_id
LEFT JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id) AND d.status IN ('pending','failed','in_flight') AND LEAST(GREATEST(d.next_attempt_at,dst.next_send_at),d.expires_at)<=clock_timestamp()
AND (d.claim_until IS NULL OR d.claim_until<=clock_timestamp()) AND (dst.claim_until IS NULL OR dst.claim_until<=clock_timestamp())
AND NOT EXISTS(SELECT 1 FROM issue_team_slack_deliveries prev WHERE prev.destination_id=d.destination_id AND prev.destination_revision=d.destination_revision AND prev.incident_id=d.incident_id AND prev.status IN ('pending','failed','in_flight') AND prev.delivery_seq<d.delivery_seq)
ORDER BY d.delivery_seq LIMIT 1;
-- name: IssueSlackClaim :exec
UPDATE issue_team_slack_deliveries SET status='in_flight',attempt=attempt+1,total_attempts=total_attempts+1,claim_id=$2,claim_until=$3,updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackDestinationLease :exec
UPDATE issue_team_slack_destinations SET claim_id=$2,claim_until=$3 WHERE id=$1;
-- name: IssueSlackTerminal :exec
UPDATE issue_team_slack_deliveries SET status=$2,last_error=$3,claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackThreadEnsure :exec
INSERT INTO issue_team_slack_threads(destination_id,destination_revision,incident_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING;
-- name: IssueSlackThreadGet :one
SELECT root_ts FROM issue_team_slack_threads WHERE destination_id=$1 AND destination_revision=$2 AND incident_id=$3;
-- name: IssueSlackThreadSet :exec
UPDATE issue_team_slack_threads SET root_ts=$4 WHERE destination_id=$1 AND destination_revision=$2 AND incident_id=$3;
-- name: IssueSlackHeld :one
SELECT d.* FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
WHERE d.id=$1 AND d.claim_id=$2 AND d.claim_until>clock_timestamp() AND d.status='in_flight'
AND dst.claim_id=d.claim_id AND dst.claim_until>clock_timestamp() AND dst.revision=d.destination_revision AND dst.enabled;
-- name: IssueSlackReady :one
SELECT EXISTS(SELECT 1 FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id JOIN accounts owner ON owner.id=a.account_id
JOIN issue_tracker_sync_notifications n ON n.id=d.notification_id
JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id AND p.enabled
WHERE d.id=$1 AND d.claim_id=$2 AND d.status='in_flight' AND d.claim_until>clock_timestamp() AND d.expires_at>clock_timestamp()
AND dst.claim_id=d.claim_id AND dst.claim_until>clock_timestamp() AND dst.enabled AND dst.revision=d.destination_revision
AND a.account_id=dst.account_id AND a.account_id=n.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
AND n.team_id=dst.team_id AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL))));
-- name: IssueSlackFinish :exec
UPDATE issue_team_slack_deliveries SET status=$2,last_error=$3,response_code=$4,next_attempt_at=$5,claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackRelease :exec
UPDATE issue_team_slack_destinations SET claim_id=NULL,claim_until=NULL,next_send_at=$3 WHERE id=$1 AND claim_id=$2;
-- name: IssueSlackDeliveryList :many
SELECT d.id,d.notification_id,d.incident_id,d.destination_revision,d.status,d.total_attempts,d.last_error,d.response_code,d.next_attempt_at,d.created_at,d.updated_at,d.channel_id,t.name AS team_name,n.kind,
 (dst.enabled AND dst.revision=d.destination_revision AND d.status IN ('failed','dead'))::boolean AS can_retry
FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id AND a.account_id=dst.account_id JOIN app_issue_teams t ON t.id=dst.team_id
JOIN issue_tracker_sync_notifications n ON n.id=d.notification_id AND n.account_id=a.account_id
WHERE a.id=$1 AND a.account_id=$2 ORDER BY d.delivery_seq DESC LIMIT $3;
-- name: IssueSlackRetryGet :one
SELECT d.*,dst.team_id,dst.revision AS current_revision,dst.enabled FROM issue_team_slack_deliveries d
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id JOIN apps a ON a.id=dst.app_id AND a.account_id=dst.account_id
WHERE d.id=$1 AND a.id=$2 AND a.account_id=$3;
-- name: IssueSlackRetry :exec
UPDATE issue_team_slack_deliveries SET status='pending',attempt=0,last_error='',next_attempt_at=clock_timestamp(),expires_at=sqlc.arg(expires_at),updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackThreadCleanup :exec
DELETE FROM issue_team_slack_threads t WHERE NOT EXISTS(SELECT 1 FROM issue_team_slack_deliveries d WHERE d.destination_id=t.destination_id AND d.destination_revision=t.destination_revision AND d.incident_id=t.incident_id);
