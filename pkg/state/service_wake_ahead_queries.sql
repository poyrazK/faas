-- Service wake-ahead (ADR-977). apid owns the setting; gatewayd-internal reads
-- it and the fleet residency that guards every wake-ahead.

-- name: GetServiceWakeAhead :one
SELECT enabled, updated_at FROM app_service_wake_ahead
WHERE app_id = sqlc.arg(app_id) AND account_id = sqlc.arg(account_id);

-- name: SetServiceWakeAhead :one
INSERT INTO app_service_wake_ahead (app_id, account_id, enabled, updated_at)
VALUES (sqlc.arg(app_id), sqlc.arg(account_id), sqlc.arg(enabled), clock_timestamp())
ON CONFLICT (app_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at
WHERE app_service_wake_ahead.account_id = EXCLUDED.account_id
RETURNING enabled, updated_at;

-- name: ServiceWakeAheadEnabled :one
-- An app that never set wake-ahead gets state.ServiceWakeAheadDefault: on for
-- a production project member under the project service policy
-- (ADR-20261025105812).
SELECT COALESCE(
    (SELECT enabled FROM app_service_wake_ahead WHERE app_id = sqlc.arg(app_id)),
    (SELECT a.project_id IS NOT NULL AND COALESCE(a.preview_of_slug, '') = ''
            AND a.manifest->>'service_binding_policy' = 'project'
       FROM apps a WHERE a.id = sqlc.arg(app_id)),
    false)::boolean AS enabled;

-- name: ServiceWakeAheadFleetResidency :one
-- Billable RAM of live instances against the active nodes' admission ceilings.
SELECT
    COALESCE((SELECT SUM(ram_mb + sqlc.arg(overhead_mb)::integer) FROM instances
              WHERE state IN ('waking', 'cold_booting', 'running', 'draining', 'warm')), 0)::bigint AS resident_mb,
    COALESCE((SELECT SUM(admission_ceiling_mb) FROM compute_nodes WHERE active), 0)::bigint AS ceiling_mb;
