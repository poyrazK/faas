-- +goose Up
ALTER TABLE job_runs
    ADD COLUMN IF NOT EXISTS image_ref_snapshot text,
    ADD COLUMN IF NOT EXISTS image_resolved_digest_snapshot text,
    ADD COLUMN IF NOT EXISTS image_storage_key_snapshot text,
    ADD COLUMN IF NOT EXISTS ram_mb_snapshot int,
    ADD COLUMN IF NOT EXISTS effective_env_snapshot jsonb,
    ADD COLUMN IF NOT EXISTS source_run_id uuid REFERENCES job_runs(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS input_manifest_uri text,
    ADD COLUMN IF NOT EXISTS input_manifest_sha256 text;

ALTER TABLE job_tasks ADD COLUMN IF NOT EXISTS source_task_index int;

UPDATE job_runs r SET
    image_ref_snapshot = j.image_ref,
    image_resolved_digest_snapshot = j.image_resolved_digest,
    image_storage_key_snapshot = j.image_storage_key,
    ram_mb_snapshot = j.ram_mb,
    effective_env_snapshot = j.env_overrides || r.env_overrides
FROM jobs j WHERE j.id = r.job_id AND r.image_ref_snapshot IS NULL;

-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_ram_snapshot_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_ram_snapshot_check CHECK (ram_mb_snapshot IS NULL OR ram_mb_snapshot > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_effective_env_snapshot_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_effective_env_snapshot_check CHECK (effective_env_snapshot IS NULL OR jsonb_typeof(effective_env_snapshot) = 'object');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_external_input_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_external_input_check CHECK (
            (input_manifest_uri IS NULL AND input_manifest_sha256 IS NULL) OR
            (input_manifest_uri IS NOT NULL AND input_manifest_sha256 ~ '^sha256:[0-9a-f]{64}$'
             AND input_manifest_version = 1));
    END IF;
END $$;
-- +goose StatementEnd

-- A run may be submitted while imaged is still materializing its source.
-- Bind its immutable artifact once the matching job source becomes ready.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_job_run_image_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.image_materialization_status = 'ready' AND NEW.image_storage_key IS NOT NULL THEN
        UPDATE job_runs SET
            image_resolved_digest_snapshot = NEW.image_resolved_digest,
            image_storage_key_snapshot = NEW.image_storage_key
        WHERE job_id = NEW.id
          AND image_ref_snapshot = NEW.image_ref
          AND image_storage_key_snapshot IS NULL;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS job_run_image_snapshot_binding ON jobs;
CREATE TRIGGER job_run_image_snapshot_binding
    AFTER UPDATE OF image_storage_key ON jobs
    FOR EACH ROW EXECUTE FUNCTION bind_job_run_image_snapshot();

-- +goose Down
DROP TRIGGER job_run_image_snapshot_binding ON jobs;
DROP FUNCTION bind_job_run_image_snapshot();
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_ram_snapshot_check,
    DROP CONSTRAINT job_runs_effective_env_snapshot_check,
    DROP CONSTRAINT job_runs_external_input_check,
    DROP COLUMN image_ref_snapshot,
    DROP COLUMN image_resolved_digest_snapshot,
    DROP COLUMN image_storage_key_snapshot,
    DROP COLUMN ram_mb_snapshot,
    DROP COLUMN effective_env_snapshot,
    DROP COLUMN source_run_id,
    DROP COLUMN input_manifest_uri,
    DROP COLUMN input_manifest_sha256;
ALTER TABLE job_tasks DROP COLUMN source_task_index;
