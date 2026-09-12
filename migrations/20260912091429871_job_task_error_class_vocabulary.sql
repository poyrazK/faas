-- +goose Up
-- +goose StatementBegin
-- The guest job supervisor and sched.HandleJobExit use the terminal status
-- vocabulary "succeeded" / "failed". Migration 00571 widened this check with
-- the legacy spelling "success" but omitted both canonical values, leaving a
-- completed guest stuck in claimed when its exit receipt reached Postgres.
-- Retain "success" for compatibility with any earlier writer while accepting
-- the values emitted by the current guest and scheduler.
ALTER TABLE job_tasks
    DROP CONSTRAINT IF EXISTS job_tasks_error_class_check;

ALTER TABLE job_tasks
    ADD CONSTRAINT job_tasks_error_class_check
    CHECK (
        error_class IS NULL
        OR error_class IN (
            'timeout',
            'refused',
            'tls_handshake',
            'dns',
            'unreachable',
            'oom',
            'user_error',
            'infra',
            'success',
            'succeeded',
            'failed',
            'cancelled',
            'job_paused',
            'oom_or_killed'
        )
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: narrowing the vocabulary would make already-persisted
-- canonical exit receipts violate the restored constraint.
SELECT 1;
-- +goose StatementEnd
