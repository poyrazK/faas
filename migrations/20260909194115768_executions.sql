-- filename: 20260909194115768_executions.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS executions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    runtime text NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    network_mode text NOT NULL DEFAULT 'none',
    timeout_ms integer NOT NULL,
    memory_mb integer NOT NULL,
    cpu_millicores integer NOT NULL,
    ephemeral_disk_mb integer NOT NULL,
    max_output_bytes integer NOT NULL,
    pids_max integer NOT NULL,
    source_bytes integer NOT NULL,
    input_bytes integer NOT NULL,
    deadline_at timestamptz NOT NULL,
    lease_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    cancel_requested_at timestamptz,
    result jsonb,
    result_bytes integer NOT NULL DEFAULT 0,
    stdout text NOT NULL DEFAULT '',
    stderr text NOT NULL DEFAULT '',
    output_truncated boolean NOT NULL DEFAULT false,
    exit_code integer,
    failure_code text,
    failure_message text,
    wall_time_ms bigint NOT NULL DEFAULT 0,
    cpu_time_ms bigint NOT NULL DEFAULT 0,
    peak_memory_mb integer NOT NULL DEFAULT 0,
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT executions_runtime_check CHECK (
        runtime IN ('node22', 'node24', 'python312', 'python313')
    ),
    CONSTRAINT executions_status_check CHECK (
        status IN ('queued', 'restoring', 'running', 'succeeded', 'failed',
                   'timed_out', 'out_of_memory', 'cancelled')
    ),
    CONSTRAINT executions_network_mode_check CHECK (network_mode = 'none'),
    CONSTRAINT executions_timeout_ms_check CHECK (timeout_ms BETWEEN 100 AND 30000),
    CONSTRAINT executions_memory_mb_check CHECK (memory_mb IN (128, 256, 512, 1024)),
    CONSTRAINT executions_cpu_millicores_check CHECK (cpu_millicores IN (250, 500, 1000)),
    CONSTRAINT executions_ephemeral_disk_mb_check CHECK (
        ephemeral_disk_mb IN (64, 128, 256, 512, 1024, 2048)
    ),
    CONSTRAINT executions_max_output_bytes_check CHECK (
        max_output_bytes BETWEEN 1024 AND 16777216
    ),
    CONSTRAINT executions_pids_max_check CHECK (pids_max = 64),
    CONSTRAINT executions_source_bytes_check CHECK (source_bytes BETWEEN 1 AND 1048576),
    CONSTRAINT executions_input_bytes_check CHECK (input_bytes BETWEEN 0 AND 1048576),
    CONSTRAINT executions_deadline_check CHECK (deadline_at > created_at),
    CONSTRAINT executions_lease_shape_check CHECK (
        (lease_token IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL)
        OR
        (lease_token IS NOT NULL AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT executions_lease_status_check CHECK (
        (status IN ('restoring', 'running')) = (lease_token IS NOT NULL)
    ),
    CONSTRAINT executions_timestamps_check CHECK (
        (status = 'queued' AND started_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'restoring' AND started_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
        OR
        (status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
            AND finished_at IS NOT NULL)
    ),
    CONSTRAINT executions_exit_code_check CHECK (exit_code IS NULL OR exit_code BETWEEN 0 AND 255),
    CONSTRAINT executions_failure_code_check CHECK (
        failure_code IS NULL OR (length(failure_code) BETWEEN 1 AND 64
            AND status IN ('failed', 'timed_out', 'out_of_memory'))
    ),
    CONSTRAINT executions_failure_message_check CHECK (
        failure_message IS NULL OR octet_length(failure_message) <= 4096
    ),
    CONSTRAINT executions_failure_shape_check CHECK (
        (failure_code IS NULL) = (failure_message IS NULL)
    ),
    CONSTRAINT executions_usage_check CHECK (
        wall_time_ms >= 0 AND cpu_time_ms >= 0 AND peak_memory_mb >= 0
    ),
    CONSTRAINT executions_result_status_check CHECK (result IS NULL OR status = 'succeeded'),
    CONSTRAINT executions_result_bytes_check CHECK (
        (result IS NULL AND result_bytes = 0)
        OR (result IS NOT NULL AND result_bytes BETWEEN 1 AND max_output_bytes)
    ),
    CONSTRAINT executions_output_budget_check CHECK (
        octet_length(stdout) + octet_length(stderr) + result_bytes <= max_output_bytes
    ),
    CONSTRAINT executions_updated_at_check CHECK (updated_at >= created_at),
    CONSTRAINT executions_lifecycle_order_check CHECK (
        (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= created_at)
        AND (started_at IS NULL OR finished_at IS NULL OR finished_at >= started_at)
        AND (cancel_requested_at IS NULL OR cancel_requested_at >= created_at)
        AND (lease_expires_at IS NULL OR lease_expires_at > updated_at)
    )
);

CREATE TABLE IF NOT EXISTS execution_payloads (
    execution_id uuid PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE,
    sealed_payload bytea NOT NULL,
    kid text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT execution_payloads_sealed_payload_check CHECK (
        octet_length(sealed_payload) BETWEEN 1 AND 3145728
    ),
    CONSTRAINT execution_payloads_kid_check CHECK (length(kid) BETWEEN 1 AND 255)
);

CREATE INDEX IF NOT EXISTS executions_account_created_idx
    ON executions (account_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS executions_account_active_idx
    ON executions (account_id, created_at)
    WHERE status IN ('queued', 'restoring', 'running');
CREATE INDEX IF NOT EXISTS executions_claim_idx
    ON executions (created_at, id)
    WHERE status = 'queued' AND cancel_requested_at IS NULL;
CREATE INDEX IF NOT EXISTS executions_lease_expiry_idx
    ON executions (lease_expires_at, id)
    WHERE status IN ('restoring', 'running');

CREATE OR REPLACE FUNCTION enforce_execution_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled') THEN
        RAISE EXCEPTION 'terminal execution % is immutable', OLD.id USING ERRCODE = '23514';
    END IF;

    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF NOT (
        (OLD.status = 'queued' AND NEW.status IN ('restoring', 'timed_out', 'cancelled'))
        OR
        (OLD.status = 'restoring' AND NEW.status IN ('queued', 'running', 'failed',
                                                     'timed_out', 'out_of_memory', 'cancelled'))
        OR
        (OLD.status = 'running' AND NEW.status IN ('succeeded', 'failed',
                                                   'timed_out', 'out_of_memory', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid execution % transition from % to %',
            OLD.id, OLD.status, NEW.status USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS executions_status_transition ON executions;
CREATE TRIGGER executions_status_transition
    BEFORE UPDATE ON executions
    FOR EACH ROW EXECUTE FUNCTION enforce_execution_status_transition();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS executions_status_transition ON executions;
DROP FUNCTION IF EXISTS enforce_execution_status_transition();
DROP TABLE IF EXISTS execution_payloads;
DROP TABLE IF EXISTS executions;
-- +goose StatementEnd
