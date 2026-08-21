-- filename: 00352_app_secret_value_hash.sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_secrets
    ADD COLUMN IF NOT EXISTS value_hash text;
-- +goose StatementEnd

-- +goose StatementBegin
DO $body$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_value_hash_shape'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_value_hash_shape
                CHECK (value_hash IS NULL OR length(value_hash) <= 16);
    END IF;
END$body$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_secrets DROP CONSTRAINT IF EXISTS app_secrets_value_hash_shape;
ALTER TABLE app_secrets DROP COLUMN IF EXISTS value_hash;
-- +goose StatementEnd
