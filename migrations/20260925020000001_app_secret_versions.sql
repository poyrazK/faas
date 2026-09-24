-- +goose Up
-- Existing rows have no reliable lifetime revision count. Keep them NULL
-- rather than presenting the delivery counter's migration baseline as history.
ALTER TABLE app_secrets ADD COLUMN IF NOT EXISTS secret_version bigint;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_secret_version_positive'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets ADD CONSTRAINT app_secrets_secret_version_positive
            CHECK (secret_version IS NULL OR secret_version >= 1);
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE app_secrets
    DROP CONSTRAINT IF EXISTS app_secrets_secret_version_positive,
    DROP COLUMN IF EXISTS secret_version;
