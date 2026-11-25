-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_vms ADD COLUMN idle_parked_at timestamptz, ADD COLUMN idle_parked_from timestamptz;
ALTER TABLE native_postgres_storage_samples ADD COLUMN low_since timestamptz;
ALTER TABLE managed_postgres_resizes ADD COLUMN automatic boolean NOT NULL DEFAULT false, ADD COLUMN auto_grow_idle_at timestamptz;

CREATE TABLE native_postgres_auto_grow (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    max_bytes bigint NOT NULL CHECK (max_bytes >= 0),
    updated_at timestamptz NOT NULL,
    checked_at timestamptz,
    reason text NOT NULL DEFAULT 'waiting_for_pressure'
);

-- Guard the shared journal even when an older worker tries to reconcile it
-- through the ordinary resize path.
-- +goose StatementBegin
CREATE FUNCTION native_postgres_auto_grow_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.volume_bytes > OLD.volume_bytes AND EXISTS (
        SELECT 1 FROM managed_postgres_resizes r
        LEFT JOIN native_postgres_auto_grow p ON p.database_id = r.database_id
        WHERE r.provider_resource_id = OLD.database_id::text
            AND r.state = 'pending' AND r.automatic AND r.target_storage_limit_bytes = NEW.volume_bytes
            AND (OLD.state <> 'parked' OR OLD.idle_parked_at IS DISTINCT FROM OLD.state_changed_at
                OR OLD.idle_parked_at IS DISTINCT FROM r.auto_grow_idle_at
                OR OLD.always_on OR OLD.replicated OR OLD.held
                OR p.max_bytes IS NULL OR p.max_bytes < NEW.volume_bytes)
    ) THEN
        RAISE EXCEPTION 'automatic storage growth requires the authorized idle park'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.state = 'waking' AND OLD.resize_pending AND EXISTS (
        SELECT 1 FROM managed_postgres_resizes r
        WHERE r.provider_resource_id = OLD.database_id::text
            AND r.state = 'pending' AND r.automatic AND r.target_storage_limit_bytes = OLD.volume_bytes
    ) THEN
        RAISE EXCEPTION 'automatic storage growth must finish before waking'
            USING ERRCODE = 'lock_not_available';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER native_postgres_auto_grow_guard
    BEFORE UPDATE OF volume_bytes, state ON database_vms
    FOR EACH ROW EXECUTE FUNCTION native_postgres_auto_grow_guard();
