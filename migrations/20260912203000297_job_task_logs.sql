-- +goose Up
-- Batch microVMs are destroyed immediately after a terminal receipt. Retain a
-- bounded combined stdout/stderr tail on the task before that cleanup removes
-- vmmd's in-memory ring.
ALTER TABLE job_tasks
    ADD COLUMN IF NOT EXISTS log_content text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS log_truncated boolean NOT NULL DEFAULT false;

ALTER TABLE job_tasks
    DROP CONSTRAINT IF EXISTS job_tasks_log_content_size_chk;
ALTER TABLE job_tasks
    ADD CONSTRAINT job_tasks_log_content_size_chk
    CHECK (octet_length(log_content) <= 1048576);

-- +goose Down
ALTER TABLE job_tasks
    DROP CONSTRAINT IF EXISTS job_tasks_log_content_size_chk,
    DROP COLUMN IF EXISTS log_truncated,
    DROP COLUMN IF EXISTS log_content;
