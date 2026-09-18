-- Extend the unified failed-events ledger to account-scoped job runs and
-- workflow runs. Jobs are account-owned rather than app-owned, so app_id is
-- nullable for those rows; workflow runs retain their app association.

-- +goose Up
-- +goose StatementBegin

ALTER TABLE dead_letter_events
    ALTER COLUMN app_id DROP NOT NULL;

ALTER TABLE dead_letter_events
    DROP CONSTRAINT IF EXISTS dead_letter_events_source_check;

ALTER TABLE dead_letter_events
    ADD CONSTRAINT dead_letter_events_source_check
    CHECK (source IN ('invocation', 'trigger_record', 'webhook_delivery', 'job_run', 'workflow_run'));

CREATE INDEX IF NOT EXISTS dead_letter_events_account_created_idx
    ON dead_letter_events (account_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS dead_letter_events_account_open_idx
    ON dead_letter_events (account_id, last_failed_at DESC, id DESC)
    WHERE replayed_at IS NULL;

-- A job run can contain more than one exhausted task. The run-level
-- projection intentionally coalesces those failures into one durable event;
-- dead_letter_count remains the authoritative number of exhausted tasks.
CREATE OR REPLACE FUNCTION faas_capture_job_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    IF NEW.dead_letter_count > COALESCE(OLD.dead_letter_count, 0) THEN
        INSERT INTO dead_letter_events (
            account_id, app_id, source, source_id, origin, event_payload,
            headers, error_kind, error_detail, retry_count,
            first_failed_at, last_failed_at, created_at
        ) VALUES (
            NEW.account_id, NULL, 'job_run', NEW.id, NEW.trigger_kind,
            jsonb_build_object(
                'job_id', NEW.job_id::text,
                'trigger_kind', NEW.trigger_kind,
                'tasks', NEW.tasks,
                'tasks_failed', NEW.tasks_failed,
                'dead_letter_count', NEW.dead_letter_count
            ),
            '{}'::jsonb,
            'job_run_dead_letter',
            jsonb_build_object(
                'dead_letter_count', NEW.dead_letter_count,
                'tasks_failed', NEW.tasks_failed
            ),
            NEW.dead_letter_count,
            COALESCE(NEW.finished_at, NOW()),
            COALESCE(NEW.finished_at, NOW()),
            COALESCE(NEW.finished_at, NOW())
        )
        ON CONFLICT (source, source_id) DO UPDATE SET
            event_payload = EXCLUDED.event_payload,
            error_detail = EXCLUDED.error_detail,
            retry_count = EXCLUDED.retry_count,
            last_failed_at = EXCLUDED.last_failed_at,
            replayed_at = NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS job_runs_capture_dead_letter_event ON job_runs;
CREATE TRIGGER job_runs_capture_dead_letter_event
    AFTER UPDATE OF dead_letter_count ON job_runs
    FOR EACH ROW EXECUTE FUNCTION faas_capture_job_dead_letter_event();

CREATE OR REPLACE FUNCTION faas_capture_workflow_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    IF NEW.status = 'dead' AND OLD.status IS DISTINCT FROM NEW.status THEN
        INSERT INTO dead_letter_events (
            account_id, app_id, source, source_id, origin, event_payload,
            headers, error_kind, error_detail, retry_count,
            first_failed_at, last_failed_at, created_at
        )
        SELECT a.account_id, NEW.app_id, 'workflow_run', NEW.id,
               NEW.workflow_name,
               jsonb_build_object(
                   'workflow_name', NEW.workflow_name,
                   'input', COALESCE(NEW.input, '{}'::jsonb),
                   'current_step', NEW.current_step
               ),
               '{}'::jsonb,
               'workflow_run_dead',
               jsonb_build_object('last_error', COALESCE(NEW.last_error, '')),
               COALESCE((
                   SELECT MAX(ws.attempt) FROM workflow_steps ws
                    WHERE ws.run_id = NEW.id
               ), 0),
               COALESCE(NEW.finished_at, NOW()),
               COALESCE(NEW.finished_at, NOW()),
               COALESCE(NEW.finished_at, NOW())
          FROM apps a
         WHERE a.id = NEW.app_id
        ON CONFLICT (source, source_id) DO UPDATE SET
            event_payload = EXCLUDED.event_payload,
            error_detail = EXCLUDED.error_detail,
            retry_count = EXCLUDED.retry_count,
            last_failed_at = EXCLUDED.last_failed_at,
            replayed_at = NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS workflow_runs_capture_dead_letter_event ON workflow_runs;
CREATE TRIGGER workflow_runs_capture_dead_letter_event
    AFTER UPDATE OF status ON workflow_runs
    FOR EACH ROW EXECUTE FUNCTION faas_capture_workflow_dead_letter_event();

INSERT INTO dead_letter_events (
    account_id, app_id, source, source_id, origin, event_payload,
    headers, error_kind, error_detail, retry_count,
    first_failed_at, last_failed_at, created_at
)
SELECT r.account_id, NULL, 'job_run', r.id, r.trigger_kind,
       jsonb_build_object(
           'job_id', r.job_id::text,
           'trigger_kind', r.trigger_kind,
           'tasks', r.tasks,
           'tasks_failed', r.tasks_failed,
           'dead_letter_count', r.dead_letter_count
       ), '{}'::jsonb, 'job_run_dead_letter',
       jsonb_build_object('dead_letter_count', r.dead_letter_count,
                          'tasks_failed', r.tasks_failed),
       r.dead_letter_count, COALESCE(r.finished_at, r.created_at),
       COALESCE(r.finished_at, r.created_at), r.created_at
  FROM job_runs r
 WHERE r.dead_letter_count > 0
ON CONFLICT (source, source_id) DO NOTHING;

INSERT INTO dead_letter_events (
    account_id, app_id, source, source_id, origin, event_payload,
    headers, error_kind, error_detail, retry_count,
    first_failed_at, last_failed_at, created_at
)
SELECT a.account_id, r.app_id, 'workflow_run', r.id, r.workflow_name,
       jsonb_build_object(
           'workflow_name', r.workflow_name,
           'input', COALESCE(r.input, '{}'::jsonb),
           'current_step', r.current_step
       ), '{}'::jsonb, 'workflow_run_dead',
       jsonb_build_object('last_error', COALESCE(r.last_error, '')),
       COALESCE((SELECT MAX(ws.attempt) FROM workflow_steps ws WHERE ws.run_id = r.id), 0),
       COALESCE(r.finished_at, r.created_at), COALESCE(r.finished_at, r.created_at),
       r.created_at
  FROM workflow_runs r
  JOIN apps a ON a.id = r.app_id
 WHERE r.status = 'dead'
ON CONFLICT (source, source_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS workflow_runs_capture_dead_letter_event ON workflow_runs;
DROP FUNCTION IF EXISTS faas_capture_workflow_dead_letter_event();
DROP TRIGGER IF EXISTS job_runs_capture_dead_letter_event ON job_runs;
DROP FUNCTION IF EXISTS faas_capture_job_dead_letter_event();
DELETE FROM dead_letter_events WHERE source IN ('job_run', 'workflow_run');
DROP INDEX IF EXISTS dead_letter_events_account_open_idx;
DROP INDEX IF EXISTS dead_letter_events_account_created_idx;
ALTER TABLE dead_letter_events
    DROP CONSTRAINT IF EXISTS dead_letter_events_source_check;
ALTER TABLE dead_letter_events
    ADD CONSTRAINT dead_letter_events_source_check
    CHECK (source IN ('invocation', 'trigger_record', 'webhook_delivery'));
ALTER TABLE dead_letter_events
    ALTER COLUMN app_id SET NOT NULL;
-- +goose StatementEnd
