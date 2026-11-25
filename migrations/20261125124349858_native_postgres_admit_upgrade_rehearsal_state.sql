-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_vms DROP CONSTRAINT database_vms_state_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_state_check CHECK (
        state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'upgrading', 'deleting', 'failed'));
