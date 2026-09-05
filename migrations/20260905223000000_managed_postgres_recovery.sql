-- filename: 20260905223000000_managed_postgres_recovery.sql

-- +goose Up
-- +goose StatementBegin
-- Retry state belongs beside the lifecycle row. Persisting it prevents API
-- retries, process restarts, or a second replica from resetting provider
-- cooldowns and multiplying upstream cost.
ALTER TABLE managed_postgres_databases
    ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 0
        CHECK (attempt_count BETWEEN 0 AND 30),
    ADD COLUMN IF NOT EXISTS retry_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS managed_postgres_databases_reconcile_idx
    ON managed_postgres_databases(retry_at, id)
    WHERE state IN ('provisioning', 'failed', 'deleting');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS managed_postgres_databases_reconcile_idx;
ALTER TABLE managed_postgres_databases
    DROP COLUMN IF EXISTS retry_at,
    DROP COLUMN IF EXISTS attempt_count;
-- +goose StatementEnd
