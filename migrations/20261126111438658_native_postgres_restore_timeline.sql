-- filename: 20261126111438658_native_postgres_restore_timeline.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases
    ADD COLUMN IF NOT EXISTS restore_recovery_timeline bigint NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'managed_postgres_databases'::regclass
            AND conname = 'managed_postgres_restore_recovery_timeline_check'
    ) THEN
        ALTER TABLE managed_postgres_databases
            ADD CONSTRAINT managed_postgres_restore_recovery_timeline_check
            CHECK (restore_recovery_timeline = 0 OR
                (restore_from_recovery AND restore_recovery_timeline BETWEEN 1 AND 4294967295)) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases DROP COLUMN restore_recovery_timeline;
