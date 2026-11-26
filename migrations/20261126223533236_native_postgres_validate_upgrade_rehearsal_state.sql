-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_vms VALIDATE CONSTRAINT database_vms_state_check_v2;

-- +goose Down
SELECT 1;
