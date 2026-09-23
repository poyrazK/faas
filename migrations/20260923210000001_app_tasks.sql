-- filename: 20260923210000001_app_tasks.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-230: durable commands attached to an immutable app deployment. Public
-- API admission remains disabled until the scheduler/VM execution path lands.
CREATE TABLE IF NOT EXISTS app_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    kind text NOT NULL,
    command text[] NOT NULL,
    command_shell boolean NOT NULL DEFAULT false,
    deployment_scope text NOT NULL,
    artifact_key text NOT NULL,
    image_digest text NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    timeout_seconds integer NOT NULL,
    max_output_bytes integer NOT NULL DEFAULT 1048576,
    lease_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    cancel_requested_at timestamptz,
    stdout_tail text NOT NULL DEFAULT '',
    stderr_tail text NOT NULL DEFAULT '',
    output_truncated boolean NOT NULL DEFAULT false,
    exit_code integer,
    failure_code text,
    failure_message text,
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_tasks_kind_chk CHECK (kind IN ('manual', 'release')),
    CONSTRAINT app_tasks_command_chk CHECK (
        cardinality(command) BETWEEN 1 AND 64
        AND array_position(command, NULL) IS NULL
        AND octet_length(command[1]) BETWEEN 1 AND 4096
        AND octet_length(array_to_string(command, '')) BETWEEN 1 AND 16384
        AND (NOT command_shell OR cardinality(command) = 1)
    ),
    CONSTRAINT app_tasks_scope_chk CHECK (
        deployment_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
    ),
    CONSTRAINT app_tasks_artifact_key_chk CHECK (
        octet_length(artifact_key) BETWEEN 1 AND 2048
    ),
    CONSTRAINT app_tasks_image_digest_chk CHECK (
        octet_length(image_digest) BETWEEN 1 AND 255
    ),
    CONSTRAINT app_tasks_status_chk CHECK (
        status IN ('queued', 'restoring', 'running', 'succeeded', 'failed',
                   'timed_out', 'cancelled')
    ),
    CONSTRAINT app_tasks_timeout_chk CHECK (timeout_seconds BETWEEN 1 AND 3600),
    CONSTRAINT app_tasks_output_budget_chk CHECK (
        max_output_bytes BETWEEN 1024 AND 16777216
        AND octet_length(stdout_tail) + octet_length(stderr_tail) <= max_output_bytes
    ),
    CONSTRAINT app_tasks_lease_shape_chk CHECK (
        (lease_token IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL)
        OR
        (lease_token IS NOT NULL AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT app_tasks_lease_status_chk CHECK (
        (status IN ('restoring', 'running')) = (lease_token IS NOT NULL)
    ),
    CONSTRAINT app_tasks_exit_code_chk CHECK (
        exit_code IS NULL OR exit_code BETWEEN 0 AND 255
    ),
    CONSTRAINT app_tasks_failure_shape_chk CHECK (
        (failure_code IS NULL) = (failure_message IS NULL)
        AND (failure_code IS NULL OR status IN ('failed', 'timed_out'))
        AND (failure_code IS NULL OR octet_length(failure_code) BETWEEN 1 AND 64)
        AND (failure_message IS NULL OR octet_length(failure_message) <= 4096)
    ),
    CONSTRAINT app_tasks_timestamps_chk CHECK (
        (status IN ('queued', 'restoring') AND started_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
        OR
        (status IN ('succeeded', 'failed', 'timed_out', 'cancelled')
            AND finished_at IS NOT NULL)
    ),
    CONSTRAINT app_tasks_lifecycle_order_chk CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= created_at)
        AND (started_at IS NULL OR finished_at IS NULL OR finished_at >= started_at)
        AND (cancel_requested_at IS NULL OR cancel_requested_at >= created_at)
        AND (lease_expires_at IS NULL OR lease_expires_at > created_at)
    )
);

CREATE INDEX IF NOT EXISTS app_tasks_account_app_created_idx
    ON app_tasks (account_id, app_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS app_tasks_claim_idx
    ON app_tasks (created_at, id)
    WHERE status = 'queued' AND cancel_requested_at IS NULL;
CREATE INDEX IF NOT EXISTS app_tasks_lease_expiry_idx
    ON app_tasks (lease_expires_at, id)
    WHERE status IN ('restoring', 'running');
CREATE UNIQUE INDEX IF NOT EXISTS app_tasks_one_release_per_deployment_uniq
    ON app_tasks (deployment_id)
    WHERE kind = 'release';

CREATE OR REPLACE FUNCTION enforce_app_task_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'timed_out', 'cancelled') THEN
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

DROP TRIGGER IF EXISTS app_tasks_status_transition ON app_tasks;
CREATE TRIGGER app_tasks_status_transition
    BEFORE UPDATE ON app_tasks
    FOR EACH ROW EXECUTE FUNCTION enforce_app_task_status_transition();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_tasks_status_transition ON app_tasks;
DROP FUNCTION IF EXISTS enforce_app_task_status_transition();
DROP TABLE IF EXISTS app_tasks;
-- +goose StatementEnd
