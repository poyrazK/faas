-- filename: 20261121140659102_run_template_firecracker_version.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE runtime_snapshots ADD COLUMN IF NOT EXISTS fc_version text NOT NULL DEFAULT '';
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'runtime_snapshots'::regclass
          AND conname = 'runtime_snapshots_fc_version_check'
    ) THEN
        ALTER TABLE runtime_snapshots ADD CONSTRAINT runtime_snapshots_fc_version_check
            CHECK (format_version = 1 OR fc_version ~ '^[0-9]+[.][0-9]+[.][0-9]+([-+][0-9A-Za-z.+-]+)?$') NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE runtime_snapshots DROP COLUMN IF EXISTS fc_version;
