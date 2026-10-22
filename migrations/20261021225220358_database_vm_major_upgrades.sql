-- filename: 20261021225220358_database_vm_major_upgrades.sql
-- migration-compat: expand

-- ADR-1027: in-place major upgrades of native databases. schedd's
-- database scheduler marks a parked database upgrading from upgrade_from
-- to upgrade_major under a lease another replica takes over once it
-- lapses, boots it on the new major with pg_upgrade, and on success makes
-- upgrade_major its postgres_major. upgrade_operation names the upgrade
-- (apid's journaled request), so a repeated request reads its outcome,
-- upgrade_result (ok, refused or failed), instead of upgrading again; the
-- last upgrade stays recorded. major_history keeps the majors the database
-- ran before, each with the instant it stopped running it: a
-- point-in-time restore before that instant recovers on that major.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE database_vms
    ADD COLUMN IF NOT EXISTS upgrade_major integer,
    ADD COLUMN IF NOT EXISTS upgrade_from integer,
    ADD COLUMN IF NOT EXISTS upgrade_operation text,
    ADD COLUMN IF NOT EXISTS upgrade_result text,
    ADD COLUMN IF NOT EXISTS upgrade_lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS major_history jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE database_vms
    DROP CONSTRAINT IF EXISTS database_vms_state_check,
    DROP CONSTRAINT IF EXISTS database_vms_upgrade,
    DROP CONSTRAINT IF EXISTS database_vms_upgrade_result_check,
    DROP CONSTRAINT IF EXISTS database_vms_major_history;

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_state_check
        CHECK (state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'upgrading', 'deleting', 'failed'));

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_upgrade_result_check
        CHECK (upgrade_result IN ('ok', 'refused', 'failed'));

-- The upgrade's fields are set together; only an upgrading database holds
-- a lease and lacks a result; a finished upgrade left the database on the
-- major its result says.
ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_upgrade
        CHECK ((upgrade_major IS NULL) = (upgrade_from IS NULL)
            AND (upgrade_major IS NULL) = (upgrade_operation IS NULL)
            AND (upgrade_major IS NULL OR (upgrade_from >= 14 AND upgrade_major > upgrade_from AND upgrade_major <= 18))
            AND (upgrade_operation IS NULL OR upgrade_operation ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
            AND (state = 'upgrading') = (upgrade_lease_until IS NOT NULL)
            AND (state <> 'upgrading' OR (upgrade_major IS NOT NULL AND upgrade_result IS NULL AND upgrade_from = postgres_major))
            AND (state = 'upgrading' OR upgrade_major IS NULL OR upgrade_result IS NOT NULL)
            AND (upgrade_result IS NULL OR upgrade_result <> 'ok' OR postgres_major = upgrade_major)
            AND (upgrade_result IS NULL OR upgrade_result = 'ok' OR postgres_major = upgrade_from));

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_major_history
        CHECK (jsonb_typeof(major_history) = 'array');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The volume of a database caught upgrading may hold either major.
UPDATE database_vms SET state = 'failed', last_error = 'upgrade_failed', upgrade_result = 'failed', upgrade_lease_until = NULL
WHERE state = 'upgrading';

ALTER TABLE database_vms
    DROP CONSTRAINT IF EXISTS database_vms_major_history,
    DROP CONSTRAINT IF EXISTS database_vms_upgrade,
    DROP CONSTRAINT IF EXISTS database_vms_upgrade_result_check,
    DROP CONSTRAINT IF EXISTS database_vms_state_check;

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_state_check
        CHECK (state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'deleting', 'failed'));

ALTER TABLE database_vms
    DROP COLUMN IF EXISTS major_history,
    DROP COLUMN IF EXISTS upgrade_lease_until,
    DROP COLUMN IF EXISTS upgrade_result,
    DROP COLUMN IF EXISTS upgrade_operation,
    DROP COLUMN IF EXISTS upgrade_from,
    DROP COLUMN IF EXISTS upgrade_major;
-- +goose StatementEnd
