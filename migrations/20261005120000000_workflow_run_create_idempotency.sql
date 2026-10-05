-- +goose Up
ALTER TABLE workflow_runs
    ADD COLUMN IF NOT EXISTS create_idempotency_key text,
    ADD COLUMN IF NOT EXISTS create_request_fingerprint bytea;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'workflow_runs'::regclass
          AND conname = 'workflow_runs_create_idempotency_check'
    ) THEN
        ALTER TABLE workflow_runs
            ADD CONSTRAINT workflow_runs_create_idempotency_check CHECK (
                (create_idempotency_key IS NULL AND create_request_fingerprint IS NULL)
                OR (
                    create_idempotency_key IS NOT NULL
                    AND octet_length(create_idempotency_key) BETWEEN 1 AND 255
                    AND create_request_fingerprint IS NOT NULL
                    AND octet_length(create_request_fingerprint) = 32
                )
            );
    END IF;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS workflow_runs_create_idempotency_idx
    ON workflow_runs (app_id, workflow_name, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS workflow_runs_create_idempotency_idx;
ALTER TABLE workflow_runs
    DROP CONSTRAINT IF EXISTS workflow_runs_create_idempotency_check,
    DROP COLUMN IF EXISTS create_idempotency_key,
    DROP COLUMN IF EXISTS create_request_fingerprint;
