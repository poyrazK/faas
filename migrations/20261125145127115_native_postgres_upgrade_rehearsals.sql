-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS database_upgrade_rehearsals (
    drill_id uuid PRIMARY KEY REFERENCES database_restore_drills (drill_id) ON DELETE CASCADE,
    request_id uuid NOT NULL,
    restore_started boolean NOT NULL DEFAULT false,
    source_major integer NOT NULL CHECK (source_major >= 15),
    target_major integer NOT NULL CHECK (target_major > source_major),
    database_name text NOT NULL,
    phase text NOT NULL DEFAULT 'restore' CHECK (phase IN ('restore', 'mark', 'upgrade', 'verify'))
);

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_state_check_v2') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_state_check_v2 CHECK (
            state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'upgrading', 'deleting', 'failed', 'rehearsal_restore', 'maintenance_handoff', 'regional_restore')) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM database_upgrade_rehearsals r
        JOIN database_restore_drills d USING (drill_id)
        WHERE d.finished_at IS NULL
    ) THEN
        RAISE EXCEPTION 'active PostgreSQL upgrade rehearsals must finish before removing their catalog';
    END IF;
END;
$$;
-- +goose StatementEnd
DELETE FROM database_restore_drills WHERE drill_id IN (SELECT drill_id FROM database_upgrade_rehearsals);
DROP TABLE database_upgrade_rehearsals;
ALTER TABLE database_vms DROP CONSTRAINT database_vms_state_check_v2;
