-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_vms ADD COLUMN restore_timeline bigint NOT NULL DEFAULT 0;
ALTER TABLE database_vms ADD CONSTRAINT database_vms_restore_timeline_check
    CHECK (restore_timeline BETWEEN 0 AND 4294967295 AND (restore_timeline = 0 OR restore_source IS NOT NULL)) NOT VALID;

ALTER TABLE database_upgrade_rehearsals ADD COLUMN recovery_timeline bigint NOT NULL DEFAULT 0;
ALTER TABLE database_upgrade_rehearsals ADD CONSTRAINT database_upgrade_rehearsals_recovery_timeline_check
    CHECK (recovery_timeline BETWEEN 0 AND 4294967295) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_upgrade_rehearsals DROP COLUMN recovery_timeline;
ALTER TABLE database_vms DROP COLUMN restore_timeline;
