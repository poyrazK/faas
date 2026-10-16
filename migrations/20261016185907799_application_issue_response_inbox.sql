-- +goose Up
CREATE OR REPLACE VIEW issue_application_response_access AS
SELECT i.id AS issue_id,a.id AS app_id,a.account_id AS owner_account_id,actor.id AS actor_account_id,owner.plan AS owner_plan,
 CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END::uuid AS responsible_team_id,route.team_id AS routed_team_id
FROM app_issues i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
JOIN accounts owner ON owner.id=a.account_id
LEFT JOIN issue_application_triage tr ON tr.issue_id=i.id
LEFT JOIN issue_application_slack_policies p ON p.app_id=a.id AND p.enabled
LEFT JOIN LATERAL (
 SELECT dst.team_id FROM issue_team_slack_destinations dst
 WHERE p.app_id IS NOT NULL AND dst.app_id=a.id AND dst.account_id=a.account_id AND dst.enabled
 AND dst.team_id IN((CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END),p.fallback_team_id)
 AND EXISTS(SELECT 1 FROM app_issue_team_members member WHERE member.team_id=dst.team_id AND (member.account_id=a.account_id OR EXISTS(
 SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=member.account_id AND m.removed_at IS NULL)))
 ORDER BY CASE WHEN dst.team_id=(CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END) THEN 0 ELSE 1 END LIMIT 1
) route ON true
JOIN accounts actor ON actor.id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=actor.id AND m.removed_at IS NULL)
WHERE a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL
AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
AND (actor.id=a.account_id OR actor.id=i.assignee_account_id OR EXISTS(
 SELECT 1 FROM app_issue_team_members tm WHERE tm.account_id=actor.id AND
 (tm.team_id=i.assignee_team_id OR (tr.escalated_at IS NOT NULL AND tm.team_id=tr.escalation_team_id) OR tm.team_id=route.team_id)));

-- +goose Down
DROP VIEW issue_application_response_access;
