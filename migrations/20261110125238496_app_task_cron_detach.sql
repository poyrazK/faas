-- migration-compat: expand
-- Hunt 32 H32-46: deleting a command cron that has ever run failed with
-- "terminal app task ... is immutable" (SQLSTATE 23514), because DeleteCron
-- detaches the cron's run history (app_tasks.cron_id = NULL) and the status
-- trigger refused any change to a terminal task. Allow exactly that detach;
-- every other column of a terminal task stays immutable.
-- Body copied from schema.sql at origin/main a3e31d54c3.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.enforce_app_task_status_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
        -- Deleting a cron detaches its run history (Hunt 32 H32-46): a
        -- terminal task may drop its cron_id and nothing else, so its
        -- outcome evidence stays immutable.
        IF OLD.cron_id IS NOT NULL AND NEW.cron_id IS NULL
           AND (to_jsonb(NEW) - 'cron_id') = (to_jsonb(OLD) - 'cron_id') THEN
            RETURN NEW;
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
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.enforce_app_task_status_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
$$;
-- +goose StatementEnd
