-- Source metadata and read state share one query snapshot. No payloads, customer
-- text, credentials, prices or execution-row locks are involved.
-- name: ProductNotificationList :many
WITH events AS (
 SELECT 'work'::text AS source_kind,r.operation_id AS source_id,'work_'||r.resolved_state AS kind,
 r.feature,''::text AS help_kind,r.resolved_at AS occurred_at,
 'work:'||r.resolved_state||':'||(extract(epoch FROM r.resolved_at)*1000000)::bigint::text AS source_version
 FROM product_control_receipts r
 WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id)
 AND sqlc.arg(can_view_work)::boolean AND r.resolved_state<>'' AND r.resolved_at>sqlc.arg(window_start)::timestamptz
 AND (sqlc.arg(source_filter)::text='' OR (sqlc.arg(source_filter)='work' AND r.operation_id=sqlc.narg(source_id_filter)::uuid))
 UNION ALL
 SELECT 'work',r.operation_id,'work_attention',r.feature,'',(o.record->>'updated_at')::timestamptz,
 'work:requires_reconciliation:'||(extract(epoch FROM (o.record->>'updated_at')::timestamptz)*1000000)::bigint::text
 FROM product_control_receipts r JOIN customer_operations o
 ON o.id=r.operation_id AND o.account_id=r.account_id AND o.app_id=r.app_id AND o.platform_tenant_id=r.tenant_id
 WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id) AND r.tenant_id=sqlc.arg(tenant_id)
 AND sqlc.arg(can_view_work)::boolean AND r.resolved_state='' AND o.state='requires_reconciliation'
 AND o.expires_at>sqlc.arg(observed_at)::timestamptz AND (o.record->>'updated_at')::timestamptz>sqlc.arg(window_start)::timestamptz
 AND (sqlc.arg(source_filter)::text='' OR (sqlc.arg(source_filter)='work' AND r.operation_id=sqlc.narg(source_id_filter)::uuid))
 UNION ALL
 SELECT 'help',h.id,CASE WHEN h.status='resolved' THEN 'help_resolved' ELSE 'help_replied' END,
 ''::text,h.kind,h.updated_at,'help:'||h.version::text
 FROM product_control_help_requests h
 WHERE h.account_id=sqlc.arg(account_id) AND h.app_id=sqlc.arg(app_id) AND h.tenant_id=sqlc.arg(tenant_id)
 AND h.auth_app_id=sqlc.arg(auth_app_id) AND h.auth_organization_id=sqlc.arg(organization_id)
 AND h.version>1 AND h.updated_at>sqlc.arg(window_start)::timestamptz
 AND (h.kind='workspace' OR (h.kind='run' AND sqlc.arg(can_view_work)::boolean) OR (h.kind='feature' AND sqlc.arg(can_view_feature)::boolean))
 AND (sqlc.arg(source_filter)::text='' OR (sqlc.arg(source_filter)='help' AND h.id=sqlc.narg(source_id_filter)::uuid))
), states AS (
 SELECT e.*,e.source_kind||'/'||e.source_id::text AS notification_id,
 coalesce(a.is_read AND a.source_version=e.source_version,false) AS is_read,
 coalesce(a.version,0)::bigint AS read_version
 FROM events e LEFT JOIN product_control_notification_reads a
 ON a.account_id=sqlc.arg(account_id) AND a.app_id=sqlc.arg(app_id) AND a.tenant_id=sqlc.arg(tenant_id)
 AND a.auth_app_id=sqlc.arg(auth_app_id) AND a.auth_organization_id=sqlc.arg(organization_id)
 AND a.source_kind=e.source_kind AND a.source_id=e.source_id AND a.expires_at>sqlc.arg(observed_at)::timestamptz
), counts AS (SELECT count(*) FILTER(WHERE NOT is_read)::bigint AS unread_count FROM states)
SELECT c.unread_count,coalesce(e.notification_id,'')::text AS notification_id,coalesce(e.kind,'')::text AS kind,
 coalesce(e.feature,'')::text AS feature,coalesce(e.help_kind,'')::text AS help_kind,
 coalesce(e.occurred_at,'1970-01-01'::timestamptz)::timestamptz AS occurred_at,
 coalesce(e.source_version,'')::text AS source_version,coalesce(e.is_read,false)::boolean AS is_read,
 coalesce(e.read_version,0)::bigint AS read_version
FROM counts c LEFT JOIN LATERAL (
 SELECT * FROM states
 WHERE (sqlc.arg(unread_only)::boolean=false OR NOT is_read)
 AND (sqlc.arg(after_key)::text='' OR (occurred_at,notification_id COLLATE "C") < (sqlc.arg(after_at)::timestamptz,sqlc.arg(after_key)::text COLLATE "C"))
 ORDER BY occurred_at DESC,notification_id COLLATE "C" DESC LIMIT sqlc.arg(page_limit)
) e ON true
ORDER BY e.occurred_at DESC,e.notification_id COLLATE "C" DESC;

-- name: ProductNotificationPrune :exec
DELETE FROM product_control_notification_reads WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3 AND expires_at<=$4;

-- name: ProductNotificationCountReads :one
SELECT count(*)::bigint FROM product_control_notification_reads WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3;

-- The customer transaction holds the existing account lock for CAS and quota.
-- name: ProductNotificationWriteRead :execrows
INSERT INTO product_control_notification_reads(account_id,app_id,tenant_id,auth_app_id,auth_organization_id,source_kind,source_id,source_version,is_read,version,updated_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(account_id,app_id,tenant_id,auth_app_id,auth_organization_id,source_kind,source_id)
DO UPDATE SET source_version=excluded.source_version,is_read=excluded.is_read,version=excluded.version,updated_at=excluded.updated_at,expires_at=excluded.expires_at
WHERE product_control_notification_reads.version=excluded.version-1;
