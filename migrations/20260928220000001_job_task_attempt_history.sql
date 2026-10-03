-- +goose Up
-- A task row is the dispatch projection. This journal retains each completed
-- attempt before the projection is reused for a retry.
CREATE TABLE IF NOT EXISTS job_task_attempts (
    run_id uuid NOT NULL,
    task_index int NOT NULL,
    attempt int NOT NULL CHECK (attempt >= 1),
    status text NOT NULL CHECK (status IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom')),
    instance_id uuid,
    error_class text,
    error_message text,
    exit_code int,
    started_at timestamptz,
    finished_at timestamptz NOT NULL,
    log_content text NOT NULL DEFAULT '',
    log_truncated boolean NOT NULL DEFAULT false,
    output_manifest jsonb,
    PRIMARY KEY (run_id, task_index, attempt),
    FOREIGN KEY (run_id, task_index) REFERENCES job_tasks (run_id, task_index) ON DELETE CASCADE,
    CONSTRAINT job_task_attempt_output_check CHECK (output_manifest IS NULL OR status = 'succeeded')
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_job_task_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.attempt > OLD.attempt THEN
        -- A claimed VM may fail before a terminal task row is written. Its
        -- retry transition still consumes an attempt and needs a result.
        INSERT INTO job_task_attempts (
            run_id, task_index, attempt, status, instance_id, error_class,
            error_message, exit_code, started_at, finished_at, log_content,
            log_truncated, output_manifest)
        VALUES (
            OLD.run_id, OLD.task_index, OLD.attempt,
            CASE WHEN OLD.status = 'claimed' THEN
                CASE WHEN NEW.error_class = 'infra' THEN 'failed' ELSE 'timeout' END
                ELSE OLD.status END,
            OLD.instance_id,
            CASE WHEN OLD.status = 'claimed' THEN 'infra' ELSE OLD.error_class END,
            CASE WHEN OLD.status = 'claimed' THEN
                COALESCE(NEW.error_message, 'reaper reclaimed stale lease')
                ELSE OLD.error_message END,
            CASE WHEN OLD.status = 'claimed' THEN
                CASE WHEN NEW.error_class = 'infra' THEN 1 ELSE 124 END
                ELSE OLD.exit_code END,
            OLD.started_at, COALESCE(OLD.finished_at, clock_timestamp()),
            OLD.log_content, OLD.log_truncated, OLD.output_manifest)
        ON CONFLICT (run_id, task_index, attempt) DO NOTHING;
    ELSIF NEW.status IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom')
       AND OLD.status NOT IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom') THEN
        INSERT INTO job_task_attempts (
            run_id, task_index, attempt, status, instance_id, error_class,
            error_message, exit_code, started_at, finished_at, log_content,
            log_truncated, output_manifest)
        VALUES (
            NEW.run_id, NEW.task_index, NEW.attempt, NEW.status, NEW.instance_id,
            NEW.error_class, NEW.error_message, NEW.exit_code, NEW.started_at,
            COALESCE(NEW.finished_at, clock_timestamp()), NEW.log_content,
            NEW.log_truncated, NEW.output_manifest)
        ON CONFLICT (run_id, task_index, attempt) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS job_task_attempt_journal ON job_tasks;
CREATE TRIGGER job_task_attempt_journal AFTER UPDATE ON job_tasks
    FOR EACH ROW EXECUTE FUNCTION record_job_task_attempt();

INSERT INTO job_task_attempts (
    run_id, task_index, attempt, status, instance_id, error_class,
    error_message, exit_code, started_at, finished_at, log_content,
    log_truncated, output_manifest)
SELECT run_id, task_index, attempt, status, instance_id, error_class,
       error_message, exit_code, started_at, COALESCE(finished_at, created_at),
       log_content, log_truncated, output_manifest
  FROM job_tasks
 WHERE status IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom')
ON CONFLICT (run_id, task_index, attempt) DO NOTHING;

-- +goose Down
DROP TRIGGER job_task_attempt_journal ON job_tasks;
DROP FUNCTION record_job_task_attempt();
DROP TABLE job_task_attempts;
