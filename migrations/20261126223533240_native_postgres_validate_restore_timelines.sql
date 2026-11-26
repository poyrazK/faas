-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_vms VALIDATE CONSTRAINT database_vms_restore_timeline_check;
ALTER TABLE database_upgrade_rehearsals VALIDATE CONSTRAINT database_upgrade_rehearsals_recovery_timeline_check;

-- +goose Down
SELECT 1;
