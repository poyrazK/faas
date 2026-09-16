-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_upload_completions
    ADD COLUMN IF NOT EXISTS idempotency_key text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS request_fingerprint text NOT NULL DEFAULT '';

ALTER TABLE object_upload_completions
    DROP CONSTRAINT IF EXISTS object_upload_completions_status_check;
ALTER TABLE object_upload_completions
    ADD CONSTRAINT object_upload_completions_status_check
    CHECK (status IN ('pending','completed','rejected','failed'));

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
         WHERE conname = 'object_upload_completions_idempotency_key_check'
           AND conrelid = 'object_upload_completions'::regclass
    ) THEN
        ALTER TABLE object_upload_completions
            ADD CONSTRAINT object_upload_completions_idempotency_key_check
            CHECK (length(idempotency_key) <= 128);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
         WHERE conname = 'object_upload_completions_request_fingerprint_check'
           AND conrelid = 'object_upload_completions'::regclass
    ) THEN
        ALTER TABLE object_upload_completions
            ADD CONSTRAINT object_upload_completions_request_fingerprint_check
            CHECK (length(request_fingerprint) <= 64);
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS object_upload_completions_idempotency_idx
    ON object_upload_completions(route_id, subject_id, idempotency_key)
    WHERE idempotency_key <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS object_upload_completions_idempotency_idx;
ALTER TABLE object_upload_completions
    DROP CONSTRAINT IF EXISTS object_upload_completions_idempotency_key_check,
    DROP CONSTRAINT IF EXISTS object_upload_completions_request_fingerprint_check;
ALTER TABLE object_upload_completions
    DROP CONSTRAINT IF EXISTS object_upload_completions_status_check;
ALTER TABLE object_upload_completions
    ADD CONSTRAINT object_upload_completions_status_check
    CHECK (status IN ('completed','rejected','failed'));
ALTER TABLE object_upload_completions
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS request_fingerprint;
-- +goose StatementEnd
