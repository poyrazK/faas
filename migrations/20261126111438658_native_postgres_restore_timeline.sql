-- filename: 20261126111438658_native_postgres_restore_timeline.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases
    ADD COLUMN restore_recovery_timeline bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT managed_postgres_restore_recovery_timeline_check
        CHECK (restore_recovery_timeline = 0 OR
            (restore_from_recovery AND restore_recovery_timeline BETWEEN 1 AND 4294967295)) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases DROP COLUMN restore_recovery_timeline;
