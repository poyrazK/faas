-- ADR-1193 webhook-submitted customer Operations.

-- name: GetOperationWebhookBinding :one
SELECT * FROM customer_operation_webhook_bindings WHERE endpoint_id = $1;

-- name: LockOperationWebhookBinding :one
SELECT * FROM customer_operation_webhook_bindings WHERE endpoint_id = $1 FOR SHARE;

-- name: SaveOperationWebhookBinding :one
INSERT INTO customer_operation_webhook_bindings(endpoint_id, account_id, app_id, scope, operation_name, platform_tenant_id, event_types, version)
VALUES ($1, $2, $3, $4, $5, $6, $7, nextval('customer_operation_webhook_binding_revision_seq'))
ON CONFLICT (endpoint_id) DO UPDATE SET scope = EXCLUDED.scope, operation_name = EXCLUDED.operation_name,
    platform_tenant_id = EXCLUDED.platform_tenant_id, event_types = EXCLUDED.event_types,
    version = EXCLUDED.version, updated_at = clock_timestamp()
WHERE customer_operation_webhook_bindings.account_id = EXCLUDED.account_id
  AND customer_operation_webhook_bindings.app_id = EXCLUDED.app_id
RETURNING *;

-- name: DeleteOperationWebhookBinding :execrows
DELETE FROM customer_operation_webhook_bindings WHERE endpoint_id = $1 AND account_id = $2 AND version = $3;

-- name: OperationWebhookTenantStatus :one
SELECT t.status, EXISTS (
    SELECT 1 FROM tenant_surfaces s
    WHERE s.account_id = t.account_id AND s.app_id = sqlc.arg(app_id) AND s.platform_tenant_id = t.id AND s.status = 'active'
)::boolean AS linked
FROM platform_tenants t
WHERE t.account_id = sqlc.arg(account_id) AND t.id = sqlc.arg(tenant_id)
FOR SHARE OF t;

-- name: GetOperationWebhookReceipt :one
SELECT * FROM customer_operation_webhook_receipts
WHERE endpoint_id = $1 AND provider_event_id = $2 AND expires_at > clock_timestamp();

-- Concurrent deliveries of one provider event serialize here; distinct events
-- on the same endpoint do not contend.
-- name: LockOperationWebhookEvent :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: InsertOperationWebhookReceipt :execrows
INSERT INTO customer_operation_webhook_receipts(endpoint_id, provider_event_id, body_sha256, account_id, operation_id, binding_version, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (endpoint_id, provider_event_id) DO UPDATE SET body_sha256 = EXCLUDED.body_sha256,
    account_id = EXCLUDED.account_id, operation_id = EXCLUDED.operation_id, binding_version = EXCLUDED.binding_version,
    accepted_at = clock_timestamp(), expires_at = EXCLUDED.expires_at
WHERE customer_operation_webhook_receipts.expires_at <= clock_timestamp();

-- name: DeleteExpiredOperationWebhookReceipts :execrows
DELETE FROM customer_operation_webhook_receipts
WHERE ctid IN (
    SELECT r.ctid FROM customer_operation_webhook_receipts r
    WHERE r.endpoint_id = sqlc.arg(endpoint_id) AND r.expires_at <= clock_timestamp()
    LIMIT sqlc.arg(page_limit)::int
);
