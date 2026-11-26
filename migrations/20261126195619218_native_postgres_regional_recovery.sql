-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_nodes
    ADD COLUMN IF NOT EXISTS recovery_region text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_nodes'::regclass
        AND conname = 'database_nodes_recovery_region_check') THEN
        ALTER TABLE database_nodes ADD CONSTRAINT database_nodes_recovery_region_check
            CHECK (recovery_region = '' OR recovery_region ~ '^[a-z0-9][a-z0-9-]{0,62}$') NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE database_vms
    ADD COLUMN IF NOT EXISTS recovery_region text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS restore_from_recovery boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_recovery_region_check') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_recovery_region_check
            CHECK (recovery_region = '' OR recovery_region ~ '^[a-z0-9][a-z0-9-]{0,62}$') NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_restore_from_recovery_check') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_restore_from_recovery_check
            CHECK (NOT restore_from_recovery OR (recovery_region <> '' AND restore_source IS NOT NULL)) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE managed_postgres_databases
    ADD COLUMN IF NOT EXISTS recovery_region text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS restore_from_recovery boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass
        AND conname = 'managed_postgres_recovery_region_check') THEN
        ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_recovery_region_check
            CHECK (recovery_region = '' OR (recovery_region <> region AND recovery_region ~ '^[a-z0-9][a-z0-9-]{0,62}$')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass
        AND conname = 'managed_postgres_restore_from_recovery_check') THEN
        ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_restore_from_recovery_check
            CHECK (NOT restore_from_recovery OR (restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL)) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_databases DROP COLUMN restore_from_recovery, DROP COLUMN recovery_region;
ALTER TABLE database_vms DROP COLUMN restore_from_recovery, DROP COLUMN recovery_region;
ALTER TABLE database_nodes DROP COLUMN recovery_region;
