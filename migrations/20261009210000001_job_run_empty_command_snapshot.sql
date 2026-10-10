-- Empty command snapshots are valid when an image supplies its own ENTRYPOINT
-- or CMD. GitOps scheduled Jobs intentionally use that image-defined command.
-- +goose Up
ALTER TABLE job_runs DROP CONSTRAINT IF EXISTS job_runs_command_shape_check;
ALTER TABLE job_runs ADD CONSTRAINT job_runs_command_shape_check
    CHECK (command IS NULL OR cardinality(command) BETWEEN 0 AND 64);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM job_runs WHERE cardinality(command)=0) THEN
        RAISE EXCEPTION 'job runs with empty command snapshots exist; refusing to restore the narrower constraint';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE job_runs DROP CONSTRAINT IF EXISTS job_runs_command_shape_check;
ALTER TABLE job_runs ADD CONSTRAINT job_runs_command_shape_check
    CHECK (command IS NULL OR cardinality(command) BETWEEN 1 AND 64);
