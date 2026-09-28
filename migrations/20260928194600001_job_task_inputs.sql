-- +goose Up
ALTER TABLE job_tasks
    ADD COLUMN input_id text,
    ADD COLUMN input_ref text,
    ADD CONSTRAINT job_tasks_input_binding_check CHECK (
        (input_id IS NULL AND input_ref IS NULL) OR
        (input_id IS NOT NULL AND input_ref IS NOT NULL
         AND length(input_id) BETWEEN 1 AND 128
         AND length(input_ref) BETWEEN 1 AND 2048)
    );
ALTER TABLE job_runs
    ADD COLUMN input_manifest_version int NOT NULL DEFAULT 0,
    ADD COLUMN input_digest text,
    ADD CONSTRAINT job_runs_input_manifest_check CHECK (
        (input_manifest_version = 0 AND input_digest IS NULL) OR
        (input_manifest_version = 1 AND input_digest ~ '^sha256:[0-9a-f]{64}$')
    );
CREATE UNIQUE INDEX job_tasks_run_input_id_unique
    ON job_tasks (run_id, input_id) WHERE input_id IS NOT NULL;

-- +goose Down
DROP INDEX job_tasks_run_input_id_unique;
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_input_manifest_check,
    DROP COLUMN input_digest,
    DROP COLUMN input_manifest_version;
ALTER TABLE job_tasks
    DROP CONSTRAINT job_tasks_input_binding_check,
    DROP COLUMN input_ref,
    DROP COLUMN input_id;
