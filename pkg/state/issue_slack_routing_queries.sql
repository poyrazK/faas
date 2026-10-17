-- name: IssueSlackRoutingPolicy :one
SELECT COALESCE(p.revision,0)::bigint AS revision,COALESCE(p.installation_revision,0)::bigint AS installation_revision,
COALESCE(p.rules,'[]'::jsonb)::jsonb AS rules,
COALESCE(p.account_id=a.account_id AND i.status='connected' AND i.account_id=a.account_id AND i.revision=p.installation_revision,false)::boolean AS active
FROM apps a LEFT JOIN issue_slack_routing_policies p ON p.app_id=a.id LEFT JOIN issue_slack_installations i ON i.app_id=a.id
WHERE a.id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueSlackRoutingPut :exec
INSERT INTO issue_slack_routing_policies(app_id,account_id,revision,installation_revision,rules) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,revision=excluded.revision,installation_revision=excluded.installation_revision,rules=excluded.rules;
-- name: IssueSlackRoutingRelease :exec
UPDATE issue_team_slack_destinations dst SET claim_id=NULL,claim_until=NULL WHERE dst.app_id=$1 AND dst.claim_id IN
(SELECT d.claim_id FROM issue_team_slack_deliveries d WHERE d.destination_id=dst.id AND d.activity_id IS NOT NULL AND d.status='in_flight' AND d.routing_revision<>$2);
-- name: IssueSlackRoutingCancel :exec
UPDATE issue_team_slack_deliveries d SET status='canceled',last_error='destination_unavailable',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
FROM issue_team_slack_destinations dst WHERE dst.id=d.destination_id AND dst.app_id=$1 AND d.activity_id IS NOT NULL AND d.routing_revision<>$2 AND d.status IN ('pending','failed','in_flight');
