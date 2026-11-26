-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS native_postgres_storage_alerts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    database_id uuid NOT NULL REFERENCES database_vms(database_id) ON DELETE CASCADE,
    incident_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('low_space', 'read_only', 'recovered')),
    sampled_at timestamptz NOT NULL,
    total_bytes bigint NOT NULL CHECK (total_bytes > 0),
    free_bytes bigint NOT NULL CHECK (free_bytes >= 0 AND free_bytes <= total_bytes),
    growth_reason text NOT NULL,
    database_name text NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token uuid,
    attempts smallint NOT NULL DEFAULT 0,
    finished_at timestamptz,
    UNIQUE (database_id, sampled_at),
    UNIQUE (incident_id, state)
);

CREATE INDEX IF NOT EXISTS native_postgres_storage_alerts_pending_idx
    ON native_postgres_storage_alerts (available_at, sampled_at) WHERE finished_at IS NULL;
CREATE INDEX IF NOT EXISTS native_postgres_storage_alerts_finished_idx
    ON native_postgres_storage_alerts (sampled_at) WHERE finished_at IS NOT NULL;

-- +goose StatementBegin
DO $create_function$
BEGIN
    IF to_regprocedure('capture_native_postgres_storage_alert()') IS NULL THEN
        CREATE FUNCTION capture_native_postgres_storage_alert() RETURNS trigger
        LANGUAGE plpgsql AS $$
        DECLARE
            previous native_postgres_storage_alerts%ROWTYPE;
            next_state text;
            next_incident uuid;
            db_name text;
            growth text;
        BEGIN
            IF NEW.sampled_at < statement_timestamp() - interval '5 minutes' THEN
                RETURN NEW;
            END IF;

            -- The upsert of the primary sample serializes this decision per VM.
            SELECT * INTO previous FROM native_postgres_storage_alerts
            WHERE database_id = NEW.database_id ORDER BY sampled_at DESC LIMIT 1;

            IF NEW.sampled_at <= previous.sampled_at THEN
                RETURN NEW;
            END IF;
            IF NEW.read_only AND previous.state IS DISTINCT FROM 'read_only' THEN
                next_state := 'read_only';
            ELSIF NEW.low AND NOT NEW.read_only AND NEW.low_since < NEW.sampled_at
                AND (previous.state IS NULL OR previous.state = 'recovered') THEN
                next_state := 'low_space';
            ELSIF NOT NEW.low AND NOT NEW.read_only AND previous.state IN ('low_space', 'read_only') THEN
                next_state := 'recovered';
            ELSE
                RETURN NEW;
            END IF;

            -- Native managed databases use the same UUID for the resource and VM.
            SELECT d.name, CASE
                WHEN next_state = 'recovered' THEN ''
                WHEN EXISTS (SELECT 1 FROM managed_postgres_resizes r WHERE r.database_id = d.id
                    AND r.state = 'pending' AND r.target_storage_limit_bytes > NEW.allocated_bytes) THEN 'resize_queued'
                WHEN next_state = 'read_only' THEN 'manual_resize_required'
                WHEN COALESCE(p.max_bytes, 0) = 0 THEN 'disabled'
                WHEN p.max_bytes <= NEW.allocated_bytes THEN 'ceiling_reached'
                WHEN p.reason IN ('storage_allowance', 'resize_conflict', 'resize_unavailable', 'node_capacity')
                    AND p.checked_at >= NEW.state_changed_at THEN p.reason
                WHEN d.scale_to_zero THEN 'waiting_for_idle'
                WHEN EXISTS (SELECT 1 FROM native_postgres_maintenance_windows w WHERE w.database_id = NEW.database_id)
                    THEN 'waiting_for_maintenance_window'
                ELSE 'maintenance_window_required'
            END INTO db_name, growth
            FROM managed_postgres_databases d LEFT JOIN native_postgres_auto_grow p ON p.database_id = d.id
            WHERE d.id = NEW.database_id AND d.provider_resource_id = NEW.database_id::text
                AND d.state IN ('ready', 'updating') AND d.clone_resource_role = 'target';
            IF NOT FOUND THEN
                RETURN NEW;
            END IF;

            next_incident := CASE WHEN previous.state IN ('low_space', 'read_only')
                THEN previous.incident_id ELSE gen_random_uuid() END;
            INSERT INTO native_postgres_storage_alerts (database_id, incident_id, state, sampled_at,
                total_bytes, free_bytes, growth_reason, database_name)
            VALUES (NEW.database_id, next_incident, next_state, NEW.sampled_at,
                NEW.total_bytes, NEW.free_bytes, growth, db_name);
            RETURN NEW;
        END;
        $$;
    END IF;
END;
$create_function$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'native_postgres_storage_samples'::regclass
        AND tgname = 'native_postgres_storage_alert_from_sample') THEN
        CREATE TRIGGER native_postgres_storage_alert_from_sample
            AFTER INSERT OR UPDATE ON native_postgres_storage_samples
            FOR EACH ROW WHEN (NEW.role = 'primary') EXECUTE FUNCTION capture_native_postgres_storage_alert();
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER native_postgres_storage_alert_from_sample ON native_postgres_storage_samples;
DROP FUNCTION capture_native_postgres_storage_alert();
DROP TABLE native_postgres_storage_alerts;
