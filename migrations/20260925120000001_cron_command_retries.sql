-- Persist opt-in bounded retry policy for deployment-attached cron commands.
-- A scheduled occurrence remains one app_tasks row; failed attempts requeue
-- that row at retry_at so existing cron history keeps one logical run.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE crons
    ADD COLUMN IF NOT EXISTS retry_max integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS retry_backoff_seconds integer NOT NULL DEFAULT 60;

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_retry_policy_check;
ALTER TABLE crons ADD CONSTRAINT crons_retry_policy_check CHECK (
    retry_max BETWEEN 0 AND 5
    AND retry_backoff_seconds BETWEEN 1 AND 3600
    AND (retry_max = 0 OR cardinality(command) > 0)
);

ALTER TABLE app_tasks
    ADD COLUMN IF NOT EXISTS retry_max integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS retry_backoff_seconds integer NOT NULL DEFAULT 60,
    ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS retry_at timestamptz;

ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_retry_policy_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_retry_policy_check CHECK (
    retry_max BETWEEN 0 AND 5
    AND retry_backoff_seconds BETWEEN 1 AND 3600
    AND attempt_count >= 0
);

ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_failure_shape_chk;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_failure_shape_chk CHECK (
    (failure_code IS NULL) = (failure_message IS NULL)
    AND (failure_code IS NULL OR status IN ('failed', 'timed_out')
         OR (status = 'queued' AND retry_at IS NOT NULL))
    AND (failure_code IS NULL OR octet_length(failure_code) BETWEEN 1 AND 64)
    AND (failure_message IS NULL OR octet_length(failure_message) <= 4096)
);

CREATE OR REPLACE FUNCTION enforce_app_task_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'timed_out', 'cancelled') THEN
        IF OLD.status IN ('failed', 'timed_out')
           AND NEW.status = 'queued'
           AND OLD.kind = 'cron'
           AND OLD.retry_max > 0
           AND OLD.attempt_count BETWEEN 1 AND OLD.retry_max
           AND NEW.attempt_count = OLD.attempt_count
           AND NEW.retry_max = OLD.retry_max
           AND NEW.cron_id IS NOT DISTINCT FROM OLD.cron_id
           AND NEW.scheduled_for IS NOT DISTINCT FROM OLD.scheduled_for
           AND NEW.retry_at > NEW.updated_at
           AND NEW.updated_at >= OLD.updated_at
           AND NEW.started_at IS NULL
           AND NEW.finished_at IS NULL
           AND NEW.lease_token IS NULL
           AND NEW.lease_owner IS NULL
           AND NEW.lease_expires_at IS NULL
           AND NEW.cancel_requested_at IS NULL THEN
            -- Retry is a lifecycle-only rewrite: all payload, ownership,
            -- command, and prior failure evidence must remain unchanged.
            -- Subtract only fields that are expected to change so any new
            -- app_tasks column remains immutable by default.
            IF (to_jsonb(NEW) - ARRAY['status', 'retry_at', 'started_at', 'finished_at', 'updated_at',
                                      'lease_token', 'lease_owner', 'lease_expires_at']) =
               (to_jsonb(OLD) - ARRAY['status', 'retry_at', 'started_at', 'finished_at', 'updated_at',
                                      'lease_token', 'lease_owner', 'lease_expires_at']) THEN
                RETURN NEW;
            END IF;
        END IF;
        IF NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'terminal app task % is immutable', OLD.id USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF NOT (
        (OLD.status = 'queued' AND NEW.status IN ('restoring', 'cancelled'))
        OR
        (OLD.status = 'restoring' AND NEW.status IN ('queued', 'running', 'failed',
                                                     'timed_out', 'cancelled'))
        OR
        (OLD.status = 'running' AND NEW.status IN ('succeeded', 'failed',
                                                   'timed_out', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid app task % transition from % to %',
            OLD.id, OLD.status, NEW.status USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END
$function$;

CREATE INDEX IF NOT EXISTS app_tasks_retry_due_idx
    ON app_tasks (retry_at, created_at, id)
    WHERE status = 'queued' AND retry_at IS NOT NULL;
-- +goose StatementEnd

-- Keep retry policy and attempt history intact if application code rolls back.
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
