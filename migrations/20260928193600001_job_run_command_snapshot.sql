-- +goose Up
ALTER TABLE job_runs
    ADD COLUMN command text[],
    ADD CONSTRAINT job_runs_command_shape_check
        CHECK (command IS NULL OR cardinality(command) BETWEEN 1 AND 64);

-- +goose Down
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_command_shape_check,
    DROP COLUMN command;
