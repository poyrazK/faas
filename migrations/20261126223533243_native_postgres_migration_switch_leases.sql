-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE release_database_switches
    ADD COLUMN IF NOT EXISTS migration_lease_token text,
    ADD COLUMN IF NOT EXISTS migration_lease_until timestamptz;

-- Old workers only see the original lease. Keep migration switches outside
-- their claim predicate while retaining the shared app limits and phases.
-- +goose StatementBegin
DO $$
DECLARE batch_size integer;
BEGIN
    LOOP
        UPDATE release_database_switches
        SET migration_lease_token = lease_token, migration_lease_until = lease_until,
            lease_token = 'migration-worker', lease_until = 'infinity'
        WHERE id IN (SELECT id FROM release_database_switches
            WHERE migration_operation_id IS NOT NULL AND lease_until IS DISTINCT FROM 'infinity'::timestamptz
            ORDER BY id LIMIT 1000);
        GET DIAGNOSTICS batch_size = ROW_COUNT;
        COMMIT;
        EXIT WHEN batch_size = 0;
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'release_database_switches'::regclass
        AND conname = 'release_database_switches_migration_lease_check') THEN
        ALTER TABLE release_database_switches ADD CONSTRAINT release_database_switches_migration_lease_check CHECK (
            (migration_operation_id IS NULL AND migration_lease_token IS NULL AND migration_lease_until IS NULL)
            OR (migration_operation_id IS NOT NULL AND lease_token IS NOT DISTINCT FROM 'migration-worker'
                AND lease_until IS NOT DISTINCT FROM 'infinity'::timestamptz
                AND (migration_lease_token IS NULL) = (migration_lease_until IS NULL))
        ) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM release_database_switches
        WHERE migration_operation_id IS NOT NULL AND phase NOT IN ('completed', 'failed', 'cancelled')) THEN
        RAISE EXCEPTION 'active native PostgreSQL migration switches require migration-aware leases';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE release_database_switches DROP CONSTRAINT IF EXISTS release_database_switches_migration_lease_check;
UPDATE release_database_switches SET lease_token = NULL, lease_until = NULL WHERE migration_operation_id IS NOT NULL;
ALTER TABLE release_database_switches
    DROP COLUMN IF EXISTS migration_lease_token,
    DROP COLUMN IF EXISTS migration_lease_until;
RESET lock_timeout;
