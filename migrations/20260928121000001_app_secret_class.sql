-- +goose Up
-- Classifies whether a secret may be retained in resumable VM snapshots.
-- Legacy rows remain persistent so existing behavior is unchanged.
ALTER TABLE app_secrets
    ADD COLUMN IF NOT EXISTS secret_class text NOT NULL DEFAULT 'persistent';

DO $body$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_secret_class_shape'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_secret_class_shape
            CHECK (secret_class IN ('persistent', 'ephemeral'));
    END IF;
END$body$;

-- +goose Down
ALTER TABLE app_secrets
    DROP CONSTRAINT IF EXISTS app_secrets_secret_class_shape,
    DROP COLUMN IF EXISTS secret_class;
