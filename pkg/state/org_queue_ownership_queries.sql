-- name: OrgQueueLockPolicy :exec
SELECT o.id FROM apps a JOIN orgs o ON o.id=a.org_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
AND NOT o.personal_org FOR UPDATE OF o;

-- name: OrgQueueLockProjectPolicy :exec
SELECT o.id FROM projects p JOIN orgs o ON o.id=p.org_id
WHERE p.id=sqlc.arg(project_id)::uuid AND p.account_id=sqlc.arg(account_id)::uuid
AND NOT o.personal_org FOR UPDATE OF o;

-- name: OrgQueueLockInvocationPolicy :exec
SELECT o.id FROM invocations i JOIN apps a ON a.id=i.app_id JOIN orgs o ON o.id=a.org_id
WHERE i.id=$1 AND NOT o.personal_org FOR UPDATE OF o;

-- name: OrgQueueReadPolicy :one
-- async_quota_pooled: the organization's async reservations use its own
-- counter. Until then they stay on the namespace counter (ADR-202610171908).
SELECT c.plan,c.status,c.abuse_hold_at,
 CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE NULL::uuid END::uuid AS owner_org_id,
 (NOT o.personal_org AND o.resource_ownership_enabled AND o.async_quota_pooled_at IS NOT NULL)::boolean AS async_quota_pooled
FROM apps a JOIN app_resource_accounts c ON c.app_id=a.id JOIN orgs o ON o.id=a.org_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid;

-- name: OrgQueueEnsureQuota :one
INSERT INTO account_async_quota(account_id,max_inflight) VALUES($1,$2)
ON CONFLICT(account_id) DO UPDATE SET max_inflight=EXCLUDED.max_inflight,updated_at=now()
RETURNING max_inflight,current_inflight;

-- name: OrgQueueCountTriggers :one
SELECT count(*)::bigint FROM triggers t JOIN apps a ON a.id=t.app_id JOIN orgs o ON o.id=a.org_id
WHERE (CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE a.account_id END)=sqlc.arg(subject)::uuid
AND a.status<>'deleted' AND (t.queue_binding_id IS NULL OR EXISTS
 (SELECT 1 FROM queue_bindings b WHERE b.id=t.queue_binding_id AND b.retired_at IS NULL AND b.mode='push'));

-- name: OrgQueueUsage :one
SELECT
 (SELECT count(*) FROM queue_bindings b WHERE b.org_id=$1 AND b.retired_at IS NULL)::bigint AS bindings,
 count(*) FILTER(WHERE i.source='queue' AND i.state='pending')::bigint AS pending,
 count(*) FILTER(WHERE i.source='queue' AND i.state='dispatching')::bigint AS processing,
 count(*) FILTER(WHERE i.source='queue' AND i.state='dead_letter')::bigint AS dead_letter,
 count(*) FILTER(WHERE i.quota_reserved)::bigint AS reserved_async
FROM apps a LEFT JOIN invocations i ON i.app_id=a.id WHERE a.org_id=$1;

-- name: OrgQueuePersonalBindingReference :one
SELECT EXISTS(SELECT 1 FROM queue_bindings b JOIN orgs o ON o.id=b.org_id
WHERE b.id=$1 AND b.account_id=$2 AND o.personal_org)::boolean AS personal;

-- name: OrgQueuePersonalInvocationReference :one
SELECT EXISTS(SELECT 1 FROM invocations i JOIN apps a ON a.id=i.app_id JOIN orgs o ON o.id=a.org_id
WHERE i.id=$1 AND i.account_id=$2 AND o.personal_org)::boolean AS personal;
