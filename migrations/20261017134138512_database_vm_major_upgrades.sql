-- filename: 20261017134138512_database_vm_major_upgrades.sql

-- ADR-1027: in-place major upgrades of native databases. schedd's
-- database scheduler marks a parked database upgrading to upgrade_major
-- under a lease another replica takes over once it lapses, boots it on
-- the new major with pg_upgrade, and on success makes upgrade_major its
-- postgres_major. major_history keeps the majors it ran before, each with
-- the instant it stopped running it: a point-in-time restore before that
-- instant recovers on that major.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE database_vms
    ADD COLUMN IF NOT EXISTS upgrade_major integer,
    ADD COLUMN IF NOT EXISTS upgrade_lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS major_history jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE database_vms
    DROP CONSTRAINT IF EXISTS database_vms_state_check,
    DROP CONSTRAINT IF EXISTS database_vms_upgrade,
    DROP CONSTRAINT IF EXISTS database_vms_major_history;

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_state_check
        CHECK (state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'upgrading', 'deleting', 'failed'));

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_upgrade
        CHECK ((state = 'upgrading') = (upgrade_major IS NOT NULL)
            AND (upgrade_major IS NULL OR (upgrade_major > postgres_major AND upgrade_major <= 18
                AND upgrade_lease_until IS NOT NULL)));

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_major_history
        CHECK (jsonb_typeof(major_history) = 'array');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The volume of a database caught upgrading may hold either major.
UPDATE database_vms SET state = 'failed', last_error = 'upgrade_failed', upgrade_major = NULL, upgrade_lease_until = NULL
WHERE state = 'upgrading';

ALTER TABLE database_vms
    DROP CONSTRAINT IF EXISTS database_vms_major_history,
    DROP CONSTRAINT IF EXISTS database_vms_upgrade,
    DROP CONSTRAINT IF EXISTS database_vms_state_check;

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_state_check
        CHECK (state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'deleting', 'failed'));

ALTER TABLE database_vms
    DROP COLUMN IF EXISTS major_history,
    DROP COLUMN IF EXISTS upgrade_lease_until,
    DROP COLUMN IF EXISTS upgrade_major;
-- +goose StatementEnd
