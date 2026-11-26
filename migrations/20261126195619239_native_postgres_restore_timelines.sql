-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_vms ADD COLUMN IF NOT EXISTS restore_timeline bigint NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_restore_timeline_check') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_restore_timeline_check
        CHECK (restore_timeline BETWEEN 0 AND 4294967295 AND (restore_timeline = 0 OR restore_source IS NOT NULL)) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE database_upgrade_rehearsals ADD COLUMN IF NOT EXISTS recovery_timeline bigint NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_upgrade_rehearsals'::regclass
        AND conname = 'database_upgrade_rehearsals_recovery_timeline_check') THEN
        ALTER TABLE database_upgrade_rehearsals ADD CONSTRAINT database_upgrade_rehearsals_recovery_timeline_check
        CHECK (recovery_timeline BETWEEN 0 AND 4294967295) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_upgrade_rehearsals DROP COLUMN recovery_timeline;
ALTER TABLE database_vms DROP COLUMN restore_timeline;
