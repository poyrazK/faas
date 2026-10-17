-- name: IssueSlackDestinationGet :one
SELECT d.* FROM issue_team_slack_destinations d JOIN apps a ON a.id=d.app_id AND a.account_id=d.account_id
WHERE d.app_id=$1 AND d.account_id=$2 AND d.team_id=$3;
-- name: IssueSlackDestinationList :many
SELECT d.id,d.team_id,d.channel_id,d.enabled,d.revision,d.oauth_revision,t.name AS team_name FROM issue_team_slack_destinations d
JOIN apps a ON a.id=d.app_id AND a.account_id=d.account_id JOIN app_issue_teams t ON t.id=d.team_id AND t.app_id=a.id
WHERE a.id=$1 AND a.account_id=$2 ORDER BY t.name,d.id;
-- name: IssueSlackDestinationPut :one
INSERT INTO issue_team_slack_destinations(id,app_id,account_id,team_id,channel_id,token_sealed,enabled) VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(app_id,team_id) DO UPDATE SET account_id=excluded.account_id,channel_id=excluded.channel_id,token_sealed=excluded.token_sealed,enabled=excluded.enabled,oauth_revision=NULL,revision=issue_team_slack_destinations.revision+1,claim_id=NULL,claim_until=NULL,next_send_at=clock_timestamp(),updated_at=clock_timestamp() RETURNING *;
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
AND NOT EXISTS(SELECT 1 FROM issue_team_slack_deliveries prev WHERE prev.destination_id=d.destination_id AND prev.destination_revision=d.destination_revision AND prev.incident_id=d.incident_id AND prev.channel_id=d.channel_id AND prev.status IN ('pending','failed','in_flight') AND prev.delivery_seq<d.delivery_seq)
ORDER BY d.delivery_seq LIMIT 1 FOR UPDATE OF a SKIP LOCKED;
-- name: IssueSlackCandidateDelivery :one
SELECT d.*,dst.app_id,dst.account_id,dst.team_id,dst.token_sealed,a.slug AS app_slug,src.issue_id,src.link_id,src.kind,src.reason,src.details::jsonb AS details,
 COALESCE(a.account_id=dst.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
 AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
 AND dst.enabled AND dst.revision=d.destination_revision AND src.eligible,false)::boolean AS eligible
FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id JOIN accounts owner ON owner.id=a.account_id
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id
WHERE a.id=sqlc.arg(app_id) AND d.status IN ('pending','failed','in_flight') AND LEAST(GREATEST(d.next_attempt_at,dst.next_send_at),d.expires_at)<=clock_timestamp()
AND (d.claim_until IS NULL OR d.claim_until<=clock_timestamp()) AND (dst.claim_until IS NULL OR dst.claim_until<=clock_timestamp())
AND NOT EXISTS(SELECT 1 FROM issue_team_slack_deliveries prev WHERE prev.destination_id=d.destination_id AND prev.destination_revision=d.destination_revision AND prev.incident_id=d.incident_id AND prev.channel_id=d.channel_id AND prev.status IN ('pending','failed','in_flight') AND prev.delivery_seq<d.delivery_seq)
ORDER BY d.delivery_seq LIMIT 1;
-- name: IssueSlackClaim :exec
UPDATE issue_team_slack_deliveries SET status='in_flight',attempt=attempt+1,total_attempts=total_attempts+1,claim_id=$2,claim_until=$3,updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackDestinationLease :exec
UPDATE issue_team_slack_destinations SET claim_id=$2,claim_until=$3 WHERE id=$1;
-- name: IssueSlackTerminal :exec
UPDATE issue_team_slack_deliveries SET status=$2,last_error=$3,claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackThreadEnsure :exec
INSERT INTO issue_team_slack_threads(destination_id,destination_revision,incident_id,channel_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING;
-- name: IssueSlackThreadGet :one
SELECT root_ts FROM issue_team_slack_threads WHERE destination_id=$1 AND destination_revision=$2 AND incident_id=$3 AND channel_id=$4;
-- name: IssueSlackThreadSet :exec
UPDATE issue_team_slack_threads SET root_ts=$5 WHERE destination_id=$1 AND destination_revision=$2 AND incident_id=$3 AND channel_id=$4;
-- name: IssueSlackHeld :one
SELECT d.* FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
WHERE d.id=$1 AND d.claim_id=$2 AND d.claim_until>clock_timestamp() AND d.status='in_flight'
AND dst.claim_id=d.claim_id AND dst.claim_until>clock_timestamp() AND dst.revision=d.destination_revision AND dst.enabled;
-- name: IssueSlackReady :one
SELECT EXISTS(SELECT 1 FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id JOIN accounts owner ON owner.id=a.account_id
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id AND src.eligible
WHERE d.id=$1 AND d.claim_id=$2 AND d.status='in_flight' AND d.claim_until>clock_timestamp() AND d.expires_at>clock_timestamp()
AND dst.claim_id=d.claim_id AND dst.claim_until>clock_timestamp() AND dst.enabled AND dst.revision=d.destination_revision
AND a.account_id=dst.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]));
-- name: IssueSlackFinish :exec
UPDATE issue_team_slack_deliveries SET status=$2,last_error=$3,response_code=$4,next_attempt_at=$5,claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackRelease :exec
UPDATE issue_team_slack_destinations SET claim_id=NULL,claim_until=NULL,next_send_at=$3 WHERE id=$1 AND claim_id=$2;
-- name: IssueSlackDeliveryList :many
SELECT d.id,d.notification_id,d.activity_id,d.reminder_id,src.issue_id,d.incident_id,d.destination_revision,d.status,d.total_attempts,d.last_error,d.response_code,d.next_attempt_at,d.created_at,d.updated_at,d.channel_id,d.routing_revision,d.routing_rule_key,t.name AS team_name,src.kind,
 (dst.enabled AND dst.revision=d.destination_revision AND d.status IN ('failed','dead') AND src.application_retry_allowed)::boolean AS can_retry
FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id AND a.account_id=dst.account_id JOIN app_issue_teams t ON t.id=dst.team_id
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id
WHERE a.id=$1 AND a.account_id=$2 ORDER BY d.delivery_seq DESC LIMIT $3;
-- name: IssueSlackRetryGet :one
SELECT d.*,dst.team_id,dst.revision AS current_revision,dst.enabled,src.application_retry_allowed::boolean AS application_retry_allowed FROM issue_team_slack_deliveries d
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id JOIN apps a ON a.id=dst.app_id AND a.account_id=dst.account_id
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id
WHERE d.id=$1 AND a.id=$2 AND a.account_id=$3;
-- name: IssueSlackRetry :exec
UPDATE issue_team_slack_deliveries SET status='pending',attempt=0,last_error='',next_attempt_at=clock_timestamp(),expires_at=sqlc.arg(expires_at),updated_at=clock_timestamp() WHERE id=$1;
-- name: IssueSlackThreadCleanup :exec
DELETE FROM issue_team_slack_threads t WHERE NOT EXISTS(SELECT 1 FROM issue_team_slack_deliveries d WHERE d.destination_id=t.destination_id AND d.destination_revision=t.destination_revision AND d.incident_id=t.incident_id AND d.channel_id=t.channel_id);

-- name: IssueApplicationSlackPolicy :one
SELECT COALESCE(p.enabled,false)::boolean AS enabled,COALESCE(p.revision,0)::bigint AS revision,
 COALESCE(p.events,ARRAY['created','regressed','impact_threshold_reached','frequency_threshold_reached']::text[])::text[] AS events,
 p.fallback_team_id,COALESCE(p.skipped_deliveries,0)::bigint AS skipped_deliveries,COALESCE(p.unrouted_alerts,0)::bigint AS unrouted_alerts
FROM apps a LEFT JOIN issue_application_slack_policies p ON p.app_id=a.id WHERE a.id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL;
-- name: IssueApplicationSlackPolicyPut :exec
INSERT INTO issue_application_slack_policies(app_id,enabled,revision,events,fallback_team_id) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(app_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision,events=excluded.events,fallback_team_id=excluded.fallback_team_id;
-- name: IssueApplicationSlackReleaseClaims :exec
UPDATE issue_team_slack_destinations dst SET claim_id=NULL,claim_until=NULL WHERE dst.app_id=$1
AND dst.claim_id IN (SELECT d.claim_id FROM issue_team_slack_deliveries d WHERE d.destination_id=dst.id AND (d.activity_id IS NOT NULL OR d.reminder_id IS NOT NULL) AND d.status='in_flight');
-- name: IssueApplicationSlackCancelPolicy :exec
UPDATE issue_team_slack_deliveries d SET status='canceled',last_error='destination_unavailable',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
FROM issue_team_slack_destinations dst WHERE dst.id=d.destination_id AND dst.app_id=$1 AND (d.activity_id IS NOT NULL OR d.reminder_id IS NOT NULL) AND d.status IN ('pending','failed','in_flight');
-- name: IssueApplicationSlackRoute :one
WITH candidates AS (
 SELECT sqlc.narg(assigned_team_id)::uuid AS team_id,0 AS priority
 UNION ALL SELECT sqlc.narg(fallback_team_id)::uuid,1
)
SELECT dst.id,dst.revision,dst.channel_id,dst.team_id,t.name AS team_name,dst.oauth_revision,array_agg(tm.account_id ORDER BY tm.account_id)::uuid[] AS recipients
FROM candidates c JOIN issue_team_slack_destinations dst ON dst.team_id=c.team_id AND dst.app_id=sqlc.arg(app_id) AND dst.account_id=sqlc.arg(account_id) AND dst.enabled
JOIN apps a ON a.id=dst.app_id AND a.account_id=dst.account_id
JOIN app_issue_teams t ON t.id=dst.team_id AND t.app_id=a.id
JOIN app_issue_team_members tm ON tm.team_id=t.id
WHERE tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)
GROUP BY dst.id,dst.revision,dst.channel_id,t.name,c.priority ORDER BY c.priority LIMIT 1;
-- name: IssueApplicationSlackHistoryTrim :execrows
DELETE FROM issue_team_slack_deliveries WHERE id IN (
 SELECT d.id FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
 WHERE dst.app_id=sqlc.arg(app_id) AND (d.activity_id IS NOT NULL OR d.reminder_id IS NOT NULL) AND d.status IN ('succeeded','dead','canceled')
 ORDER BY d.delivery_seq LIMIT 1
);
-- name: IssueApplicationSlackCount :one
SELECT count(*) FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id WHERE dst.app_id=$1 AND (d.activity_id IS NOT NULL OR d.reminder_id IS NOT NULL);
-- name: IssueApplicationSlackMissed :exec
UPDATE issue_application_slack_policies SET skipped_deliveries=skipped_deliveries+CASE WHEN sqlc.arg(capacity)::boolean THEN 1 ELSE 0 END,
 unrouted_alerts=unrouted_alerts+CASE WHEN sqlc.arg(capacity)::boolean THEN 0 ELSE 1 END WHERE app_id=sqlc.arg(app_id);
-- name: IssueApplicationSlackEnqueue :exec
INSERT INTO issue_team_slack_deliveries(id,destination_id,destination_revision,activity_id,incident_id,channel_id,application_policy_revision,recipient_account_ids,assigned_team_id,assigned_account_id,routing_revision,routing_rule_key)
VALUES(sqlc.arg(id),sqlc.arg(destination_id),sqlc.arg(destination_revision),sqlc.arg(activity_id),sqlc.arg(issue_id),sqlc.arg(channel_id),sqlc.arg(policy_revision),sqlc.arg(recipients)::uuid[],sqlc.narg(assigned_team_id),sqlc.narg(assigned_account_id),sqlc.arg(routing_revision),sqlc.arg(routing_rule_key)) ON CONFLICT DO NOTHING;
-- name: IssueApplicationSlackHistoryExpire :exec
DELETE FROM issue_team_slack_deliveries target WHERE target.status IN ('succeeded','dead','canceled') AND target.updated_at<sqlc.arg(before) AND target.id IN (
 SELECT d.id FROM issue_team_slack_deliveries d WHERE (d.activity_id IS NOT NULL OR d.reminder_id IS NOT NULL) AND d.status IN ('succeeded','dead','canceled') AND d.updated_at<sqlc.arg(before)
 ORDER BY d.delivery_seq LIMIT sqlc.arg(batch_size)
);
-- name: IssueSlackInteractionPolicy :one
SELECT p.*,a.account_id,a.slug FROM issue_slack_interaction_policies p JOIN apps a ON a.id=p.app_id
WHERE a.id=$1 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueSlackInteractionPolicyPut :exec
INSERT INTO issue_slack_interaction_policies(app_id,enabled,revision,workspace_id,slack_app_id,secret_sealed) VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(app_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision,workspace_id=excluded.workspace_id,slack_app_id=excluded.slack_app_id,secret_sealed=excluded.secret_sealed;
-- name: IssueSlackIdentitiesRevoke :exec
DELETE FROM issue_slack_identities WHERE app_id=$1;
-- name: IssueSlackActionPrepare :exec
INSERT INTO issue_slack_action_messages(delivery_id,policy_revision,response_id,response_revision,can_assign)
SELECT d.id,p.revision,tr.incident_id,COALESCE(tr.revision,0),true FROM issue_team_slack_deliveries d
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN issue_slack_interaction_policies p ON p.app_id=dst.app_id AND p.enabled
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id AND src.eligible
LEFT JOIN issue_application_triage tr ON tr.issue_id=src.issue_id
WHERE d.id=$1 AND d.claim_id=$2 AND d.status='in_flight' AND d.claim_until>clock_timestamp() AND d.notification_id IS NULL
ON CONFLICT(delivery_id) DO NOTHING;
-- name: IssueSlackActionPrepared :one
SELECT (count(*)>0)::boolean AS enabled,COALESCE(bool_or(m.can_assign),false)::boolean AS can_assign FROM issue_slack_action_messages m JOIN issue_team_slack_deliveries d ON d.id=m.delivery_id
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id JOIN issue_slack_interaction_policies p ON p.app_id=dst.app_id AND p.enabled AND p.revision=m.policy_revision
WHERE d.id=$1 AND d.claim_id=$2 AND d.status='in_flight';
-- name: IssueSlackActionSent :exec
UPDATE issue_slack_action_messages SET message_ts=$2 WHERE delivery_id=$1;
-- name: IssueSlackActionContext :one
SELECT a.id AS app_id,a.account_id,a.slug,src.issue_id,m.response_id,m.response_revision,m.consumed_at,m.can_assign,tr.incident_id AS current_response_id,COALESCE(tr.revision,0)::bigint AS current_response_revision
FROM issue_slack_action_messages m JOIN issue_team_slack_deliveries d ON d.id=m.delivery_id
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id AND dst.revision=d.destination_revision AND dst.enabled
JOIN apps a ON a.id=dst.app_id JOIN accounts owner ON owner.id=a.account_id JOIN issue_slack_interaction_policies p ON p.app_id=a.id AND p.enabled AND p.revision=m.policy_revision
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id AND src.eligible
JOIN app_issues i ON i.id=src.issue_id AND i.state='open'
LEFT JOIN issue_application_triage tr ON tr.issue_id=i.id
WHERE a.id=sqlc.arg(app_id) AND d.id=sqlc.arg(delivery_id) AND d.status='succeeded' AND d.channel_id=sqlc.arg(channel_id)
AND m.message_ts=sqlc.arg(message_ts) AND m.message_ts<>'' AND p.revision=sqlc.arg(policy_revision)
AND a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]);
-- name: IssueSlackActionConsume :exec
UPDATE issue_slack_action_messages SET consumed_at=clock_timestamp() WHERE delivery_id=$1;
-- name: IssueSlackActionsInvalidate :exec
UPDATE issue_slack_action_messages m SET consumed_at=clock_timestamp()
FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
LEFT JOIN issue_activity act ON act.id=d.activity_id
LEFT JOIN issue_application_reminders rem ON rem.id=d.reminder_id
WHERE d.id=m.delivery_id AND dst.app_id=sqlc.arg(app_id)
AND COALESCE(act.issue_id,rem.issue_id)=sqlc.arg(issue_id) AND m.consumed_at IS NULL;
-- name: IssueSlackIdentityGet :one
SELECT l.slack_user_id,l.account_id,l.can_assign FROM issue_slack_identities l JOIN issue_slack_interaction_policies p ON p.app_id=l.app_id AND p.revision=l.policy_revision AND p.enabled
JOIN accounts ac ON ac.id=l.account_id AND ac.status='active' AND ac.abuse_hold_at IS NULL
WHERE l.app_id=$1 AND l.slack_user_id=$2 AND l.expires_at>clock_timestamp()
AND l.mfa_enrolled_at IS NOT DISTINCT FROM ac.mfa_enrolled_at AND l.mfa_required=ac.mfa_required AND NOT(ac.mfa_required AND ac.mfa_enrolled_at IS NULL);
-- name: IssueSlackIdentityMine :one
SELECT l.slack_user_id,p.workspace_id,l.expires_at,l.can_assign FROM issue_slack_identities l JOIN issue_slack_interaction_policies p ON p.app_id=l.app_id AND p.revision=l.policy_revision AND p.enabled
WHERE l.app_id=$1 AND l.account_id=$2;
-- name: IssueSlackIdentityCount :one
SELECT count(*) FROM issue_slack_identities WHERE app_id=$1;
-- name: IssueSlackIdentityPut :execrows
INSERT INTO issue_slack_identities(app_id,slack_user_id,account_id,policy_revision,mfa_enrolled_at,mfa_required,expires_at,can_assign)
SELECT sqlc.arg(app_id),sqlc.arg(slack_user_id),ac.id,sqlc.arg(policy_revision),ac.mfa_enrolled_at,ac.mfa_required,sqlc.arg(expires_at),true FROM accounts ac
WHERE ac.id=sqlc.arg(account_id) AND ac.status='active' AND ac.abuse_hold_at IS NULL AND NOT(ac.mfa_required AND ac.mfa_enrolled_at IS NULL)
ON CONFLICT(app_id,slack_user_id) DO UPDATE SET policy_revision=excluded.policy_revision,mfa_enrolled_at=excluded.mfa_enrolled_at,mfa_required=excluded.mfa_required,expires_at=excluded.expires_at,can_assign=excluded.can_assign WHERE issue_slack_identities.account_id=excluded.account_id;
-- name: IssueSlackIdentityDelete :exec
DELETE FROM issue_slack_identities WHERE app_id=$1 AND account_id=$2;
-- name: IssueSlackChallengeGet :one
SELECT * FROM issue_slack_link_challenges WHERE app_id=$1 AND request_key=$2;
-- name: IssueSlackChallengeByToken :one
SELECT ch.*,src.issue_id,p.workspace_id FROM issue_slack_link_challenges ch
JOIN issue_slack_interaction_policies p ON p.app_id=ch.app_id AND p.enabled AND p.revision=ch.policy_revision
JOIN issue_team_slack_deliveries d ON d.id=ch.delivery_id AND d.status='succeeded'
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id AND dst.enabled AND dst.revision=d.destination_revision
JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id AND src.eligible
JOIN issue_slack_action_messages m ON m.delivery_id=d.id AND m.consumed_at IS NULL
JOIN app_issues i ON i.id=src.issue_id AND i.state='open'
LEFT JOIN issue_application_triage tr ON tr.issue_id=src.issue_id
WHERE ch.app_id=$1 AND ch.token=$2 AND ch.consumed_at IS NULL AND ch.expires_at>clock_timestamp()
AND m.response_id IS NOT DISTINCT FROM tr.incident_id AND m.response_revision=COALESCE(tr.revision,0);
-- name: IssueSlackChallengesExpire :exec
DELETE FROM issue_slack_link_challenges WHERE app_id=$1 AND expires_at<=clock_timestamp();
-- name: IssueSlackChallengeCount :one
SELECT count(*) FROM issue_slack_link_challenges WHERE app_id=$1;
-- name: IssueSlackChallengePut :exec
INSERT INTO issue_slack_link_challenges(token,app_id,delivery_id,request_key,slack_user_id,policy_revision,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: IssueSlackChallengeConsume :exec
UPDATE issue_slack_link_challenges SET consumed_at=clock_timestamp() WHERE token=$1;

-- name: IssueSlackIdentitiesExpire :exec
DELETE FROM issue_slack_identities WHERE app_id=$1 AND expires_at<=clock_timestamp();

-- name: IssueSlackInteractionNow :one
SELECT statement_timestamp()::timestamptz;
