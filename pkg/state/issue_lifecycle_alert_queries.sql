-- name: IssueLifecyclePolicy :one
SELECT COALESCE(p.revision,0)::bigint AS revision,
 COALESCE(p.environments,ARRAY[]::text[])::text[] AS environments,
 COALESCE(p.cooldown_seconds,0)::bigint AS cooldown_seconds
FROM apps a LEFT JOIN app_issue_lifecycle_alert_policies p ON p.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.deleted_at IS NULL;

-- name: IssueLifecyclePutPolicy :exec
INSERT INTO app_issue_lifecycle_alert_policies(app_id,revision,environments,cooldown_seconds)
VALUES(sqlc.arg(app_id),sqlc.arg(revision),sqlc.arg(environments),sqlc.arg(cooldown_seconds))
ON CONFLICT(app_id) DO UPDATE SET revision=excluded.revision,environments=excluded.environments,cooldown_seconds=excluded.cooldown_seconds;

-- name: IssueLifecycleLastAlert :one
SELECT max(last_alert_at)::timestamptz AS last_alert_at FROM issue_lifecycle_alert_receipts r
WHERE r.app_id=sqlc.arg(app_id) AND r.issue_id IN
(SELECT sqlc.arg(issue_id)::uuid UNION ALL SELECT issue_id FROM issue_group_links WHERE canonical_issue_id=sqlc.arg(issue_id));

-- name: IssueLifecycleRecordAlert :exec
INSERT INTO issue_lifecycle_alert_receipts(app_id,issue_id,last_alert_at)
VALUES(sqlc.arg(app_id),sqlc.arg(issue_id),sqlc.arg(now))
ON CONFLICT(app_id,issue_id) DO UPDATE SET last_alert_at=GREATEST(issue_lifecycle_alert_receipts.last_alert_at,excluded.last_alert_at);
