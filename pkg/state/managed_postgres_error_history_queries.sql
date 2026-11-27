-- name: SetNativePostgresErrorHistory :exec
INSERT INTO native_postgres_error_history_settings (database_id, epoch, enabled)
VALUES (@database_id, @epoch, @enabled)
ON CONFLICT (database_id) DO UPDATE SET enabled = EXCLUDED.enabled, epoch = EXCLUDED.epoch
WHERE native_postgres_error_history_settings.enabled <> EXCLUDED.enabled;

-- name: GetNativePostgresErrorHistory :one
SELECT enabled FROM native_postgres_error_history_settings WHERE database_id = @database_id;

-- name: ListNativePostgresErrorEvents :many
SELECT observed_at, severity, sqlstate, binding_id FROM native_postgres_error_events
WHERE database_id = @database_id AND observed_at >= @since
  AND (@filter_sqlstate::text = '' OR sqlstate = @filter_sqlstate)
ORDER BY observed_at DESC, id DESC LIMIT @result_limit;

-- name: GetNativePostgresErrorHistoryPolicies :many
SELECT COALESCE(NULLIF(d.data_resource_id, ''), d.provider_resource_id, '')::text AS runtime_id, s.epoch
FROM native_postgres_error_history_settings s JOIN managed_postgres_databases d ON d.id = s.database_id
WHERE s.enabled AND d.state = 'ready'
  AND COALESCE(NULLIF(d.data_resource_id, ''), d.provider_resource_id) = ANY(@runtime_ids::text[])
  AND NOT EXISTS (
      SELECT 1 FROM managed_postgres_databases other
      WHERE other.id <> d.id AND other.state = 'ready'
        AND COALESCE(NULLIF(other.data_resource_id, ''), other.provider_resource_id) =
            COALESCE(NULLIF(d.data_resource_id, ''), d.provider_resource_id)
  );

-- name: LockNativePostgresErrorHistoryPolicies :many
SELECT database_id FROM native_postgres_error_history_settings
WHERE enabled AND epoch = ANY(@epochs::uuid[])
ORDER BY database_id FOR UPDATE;

-- name: AppendNativePostgresErrorEvents :exec
INSERT INTO native_postgres_error_events (id, database_id, observed_at, severity, sqlstate, binding_id)
SELECT e.id, s.database_id, e.observed_at, e.severity, e.sqlstate, b.id
FROM jsonb_to_recordset(@events::jsonb) AS e
    (id uuid, runtime_id text, epoch uuid, observed_at timestamptz, severity text, sqlstate text, role_name text)
JOIN native_postgres_error_history_settings s ON s.enabled AND s.epoch = e.epoch
JOIN managed_postgres_databases d ON d.id = s.database_id AND d.state = 'ready'
  AND COALESCE(NULLIF(d.data_resource_id, ''), d.provider_resource_id) = e.runtime_id
LEFT JOIN LATERAL (
    SELECT id FROM managed_postgres_bindings
    WHERE database_id = d.id AND provider_identity_id = e.role_name
      AND state IN ('ready', 'provisioning', 'retiring')
    ORDER BY id LIMIT 1
) b ON true
WHERE e.observed_at BETWEEN clock_timestamp() - interval '24 hours' AND clock_timestamp() + interval '30 seconds'
  AND NOT EXISTS (
      SELECT 1 FROM managed_postgres_databases other
      WHERE other.id <> d.id AND other.state = 'ready'
        AND COALESCE(NULLIF(other.data_resource_id, ''), other.provider_resource_id) = e.runtime_id
  )
ON CONFLICT (id) DO NOTHING;

-- name: TrimNativePostgresErrorEvents :exec
DELETE FROM native_postgres_error_events
WHERE id IN (
    SELECT stale.id FROM unnest(@database_ids::uuid[]) scoped(database_id)
    CROSS JOIN LATERAL (
        SELECT id FROM native_postgres_error_events
        WHERE database_id = scoped.database_id
        ORDER BY observed_at DESC, id DESC OFFSET @retained_count
    ) stale
);

-- name: ExpireNativePostgresErrorEvents :exec
DELETE FROM native_postgres_error_events
WHERE id IN (
    SELECT id FROM native_postgres_error_events
    WHERE observed_at < clock_timestamp() - interval '24 hours'
    ORDER BY observed_at LIMIT 4096 FOR UPDATE SKIP LOCKED
);
