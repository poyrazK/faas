-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_vms ADD COLUMN IF NOT EXISTS idle_parked_at timestamptz, ADD COLUMN IF NOT EXISTS idle_parked_from timestamptz;
ALTER TABLE native_postgres_storage_samples ADD COLUMN IF NOT EXISTS low_since timestamptz;
ALTER TABLE managed_postgres_resizes ADD COLUMN IF NOT EXISTS automatic boolean NOT NULL DEFAULT false, ADD COLUMN IF NOT EXISTS auto_grow_idle_at timestamptz;

-- Guard the shared journal even when an older worker tries to reconcile it
-- through the ordinary resize path.
-- +goose StatementBegin
DO $create_function$
BEGIN
    IF to_regprocedure('native_postgres_auto_grow_guard()') IS NULL THEN
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
    END IF;
END;
$create_function$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'database_vms'::regclass
        AND tgname = 'native_postgres_auto_grow_guard') THEN
        CREATE TRIGGER native_postgres_auto_grow_guard
            BEFORE UPDATE OF volume_bytes, state ON database_vms
            FOR EACH ROW EXECUTE FUNCTION native_postgres_auto_grow_guard();
    END IF;
END;
$$;
-- +goose StatementEnd
