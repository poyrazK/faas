-- ADR-798: exact issued allocation identity, independent of today's registry.
-- name: ReadGlobalRuntimeUsageBinding :one
SELECT account_id::text AS account_id,app_slug,grant_claims,released_at
FROM global_runtime_billing_issued
WHERE global_app_id=sqlc.arg(global_app_id)::uuid AND allocation_id=sqlc.arg(allocation_id)::uuid
  AND cell_id=sqlc.arg(cell_id)::text;

-- name: ReadGlobalRuntimeUsage :one
SELECT * FROM global_runtime_usage_minutes
WHERE global_app_id=sqlc.arg(global_app_id)::uuid AND allocation_id=sqlc.arg(allocation_id)::uuid
  AND cell_id=sqlc.arg(cell_id)::uuid AND minute=sqlc.arg(minute)::timestamptz;

-- name: InsertGlobalRuntimeUsage :exec
INSERT INTO global_runtime_usage_minutes(global_app_id,allocation_id,cell_id,minute,account_id,
 app_slug,billing_instance_id,usage_claims,envelope)
VALUES(sqlc.arg(global_app_id)::uuid,sqlc.arg(allocation_id)::uuid,sqlc.arg(cell_id)::uuid,
 sqlc.arg(minute)::timestamptz,sqlc.arg(account_id)::uuid,sqlc.arg(app_slug)::text,
 sqlc.arg(billing_instance_id)::uuid,sqlc.arg(usage_claims)::jsonb,sqlc.arg(envelope)::jsonb);

-- name: InsertGlobalRuntimeCanonicalUsage :execrows
INSERT INTO usage_minutes(account_id,app_id,instance_id,minute,mb_seconds)
SELECT account_id,global_app_id,billing_instance_id,minute,(usage_claims->>'MBSeconds')::bigint
FROM global_runtime_usage_minutes
WHERE global_app_id=sqlc.arg(global_app_id)::uuid AND allocation_id=sqlc.arg(allocation_id)::uuid
  AND cell_id=sqlc.arg(cell_id)::uuid AND minute=sqlc.arg(minute)::timestamptz
ON CONFLICT(instance_id,minute) DO NOTHING;

-- name: GlobalRuntimeBillingReady :one
SELECT NOT EXISTS(
 SELECT 1 FROM global_runtime_billing_issued issued
 WHERE issued.account_id=sqlc.arg(account_id)::uuid AND issued.issued_at<sqlc.arg(window_end)::timestamptz
  AND (issued.released_at IS NULL OR issued.released_at>sqlc.arg(window_start)::timestamptz)
  AND (SELECT count(*) FROM global_runtime_usage_minutes usage
       WHERE usage.global_app_id=issued.global_app_id AND usage.allocation_id=issued.allocation_id
        AND usage.cell_id=issued.cell_id::uuid
        AND usage.minute>=greatest(sqlc.arg(window_start)::timestamptz,date_trunc('minute',issued.issued_at))
        AND usage.minute<least(sqlc.arg(window_end)::timestamptz,
          CASE WHEN issued.released_at IS NULL THEN sqlc.arg(window_end)::timestamptz
           WHEN issued.released_at=date_trunc('minute',issued.released_at) THEN issued.released_at
           ELSE date_trunc('minute',issued.released_at)+interval '1 minute' END))
   <> greatest(0,extract(epoch FROM (
       least(sqlc.arg(window_end)::timestamptz,
         CASE WHEN issued.released_at IS NULL THEN sqlc.arg(window_end)::timestamptz
          WHEN issued.released_at=date_trunc('minute',issued.released_at) THEN issued.released_at
          ELSE date_trunc('minute',issued.released_at)+interval '1 minute' END)
       - greatest(sqlc.arg(window_start)::timestamptz,date_trunc('minute',issued.issued_at))))/60)::bigint
) AS ready;
