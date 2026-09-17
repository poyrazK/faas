-- filename: 20260917172636155_managed_realtime_drain_async.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_realtime_drain_operations
    ADD COLUMN IF NOT EXISTS connection_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS drain_limit integer NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS truncated boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS partial boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS nodes_queried integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS nodes_unavailable integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS claimed_at timestamptz,
    ADD COLUMN IF NOT EXISTS claim_token uuid,
    ADD COLUMN IF NOT EXISTS last_error text NOT NULL DEFAULT '';

DO $$
BEGIN
    ALTER TABLE managed_realtime_drain_operations
        ADD CONSTRAINT managed_realtime_drain_operations_connection_ids_array
        CHECK (jsonb_typeof(connection_ids) = 'array');
EXCEPTION WHEN duplicate_object THEN NULL;
END
$$;

CREATE INDEX IF NOT EXISTS managed_realtime_drain_operations_worker_idx
    ON managed_realtime_drain_operations (next_attempt_at, created_at)
    WHERE status = 'running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS managed_realtime_drain_operations_worker_idx;
ALTER TABLE managed_realtime_drain_operations
    DROP CONSTRAINT IF EXISTS managed_realtime_drain_operations_connection_ids_array,
    DROP COLUMN IF EXISTS connection_ids,
    DROP COLUMN IF EXISTS drain_limit,
    DROP COLUMN IF EXISTS truncated,
    DROP COLUMN IF EXISTS partial,
    DROP COLUMN IF EXISTS nodes_queried,
    DROP COLUMN IF EXISTS nodes_unavailable,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS claim_token,
    DROP COLUMN IF EXISTS last_error;
-- +goose StatementEnd
