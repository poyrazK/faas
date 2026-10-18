-- name: IssueCompoundPolicy :one
SELECT COALESCE(p.revision,0)::bigint AS revision,COALESCE(p.rules,'[]'::jsonb)::jsonb AS rules
FROM apps a LEFT JOIN app_issue_compound_alert_policies p ON p.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.deleted_at IS NULL;

-- name: IssueCompoundPutPolicy :exec
INSERT INTO app_issue_compound_alert_policies(app_id,revision,rules)
VALUES(sqlc.arg(app_id),sqlc.arg(revision),sqlc.arg(rules))
ON CONFLICT(app_id) DO UPDATE SET revision=excluded.revision,rules=excluded.rules;

-- name: IssueCompoundPruneReceipts :exec
DELETE FROM issue_compound_alert_receipts WHERE app_id=sqlc.arg(app_id) AND NOT (rule_key=ANY(sqlc.arg(keys)::text[]));

-- name: IssueCompoundCount :one
SELECT count(*) AS observed_events,
count(DISTINCT COALESCE(e.verified_platform_tenant_id,e.verified_consumer_id)) AS identified_customers
FROM issue_events e
WHERE e.issue_id IN (SELECT sqlc.arg(issue_id)::uuid UNION ALL SELECT issue_id FROM issue_group_links WHERE canonical_issue_id=sqlc.arg(issue_id))
AND e.occurred_at > sqlc.arg(since) AND e.occurred_at <= sqlc.arg(until);

-- name: IssueCompoundLastAlert :one
SELECT max(last_alert_at)::timestamptz AS last_alert_at FROM issue_compound_alert_receipts r
WHERE r.app_id=sqlc.arg(app_id) AND r.rule_key=sqlc.arg(rule_key) AND r.rule_hash=sqlc.arg(rule_hash)
AND r.issue_id IN (SELECT sqlc.arg(issue_id)::uuid UNION ALL SELECT issue_id FROM issue_group_links WHERE canonical_issue_id=sqlc.arg(issue_id));

-- name: IssueCompoundRecordAlert :exec
INSERT INTO issue_compound_alert_receipts(app_id,rule_key,issue_id,rule_hash,last_alert_at)
VALUES(sqlc.arg(app_id),sqlc.arg(rule_key),sqlc.arg(issue_id),sqlc.arg(rule_hash),sqlc.arg(now))
ON CONFLICT(app_id,rule_key,issue_id) DO UPDATE SET rule_hash=excluded.rule_hash,last_alert_at=excluded.last_alert_at;

-- name: IssueCompoundSnoozed :one
SELECT EXISTS(SELECT 1 FROM issue_application_triage WHERE issue_id=sqlc.arg(issue_id) AND snoozed_until>sqlc.arg(now));
