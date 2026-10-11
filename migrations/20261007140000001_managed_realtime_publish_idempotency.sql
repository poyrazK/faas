-- +goose Up
ALTER TABLE idempotency_keys
    ADD COLUMN IF NOT EXISTS request_digest bytea;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'idempotency_keys_request_digest_chk'
          AND conrelid = 'idempotency_keys'::regclass
    ) THEN
        ALTER TABLE idempotency_keys
            ADD CONSTRAINT idempotency_keys_request_digest_chk
            CHECK (request_digest IS NULL OR octet_length(request_digest) = 32);
    END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS idempotency_keys_publish_receipts_created_at_idx
    ON idempotency_keys (created_at)
    WHERE request_digest IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idempotency_keys_publish_receipts_created_at_idx;
ALTER TABLE idempotency_keys
    DROP CONSTRAINT IF EXISTS idempotency_keys_request_digest_chk,
    DROP COLUMN IF EXISTS request_digest;
