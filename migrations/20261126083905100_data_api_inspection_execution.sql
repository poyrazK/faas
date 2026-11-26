-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS data_api_inspection_executions (
 task_id uuid PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
 protocol_version smallint NOT NULL CHECK (protocol_version = 5),
 stdout_sha256 bytea NOT NULL CHECK (octet_length(stdout_sha256) = 32),
 finished_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE data_api_inspection_executions;
