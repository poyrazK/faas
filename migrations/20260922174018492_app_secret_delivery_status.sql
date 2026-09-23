-- +goose Up
-- +goose StatementBegin
-- Track the exact app-secret version staged into a successfully started
-- runtime. Values never enter this metadata: delivery_version is an opaque
-- monotonic counter and the remaining columns contain only runtime identity
-- and timestamps.
ALTER TABLE app_secrets
    ADD COLUMN IF NOT EXISTS delivery_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS delivered_version BIGINT,
    ADD COLUMN IF NOT EXISTS delivery_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS last_delivery_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_delivered_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_delivery_error_code TEXT,
    ADD COLUMN IF NOT EXISTS last_delivered_wake_id TEXT,
    ADD COLUMN IF NOT EXISTS last_delivered_instance_id TEXT;

-- PostgreSQL has no ADD CONSTRAINT IF NOT EXISTS. Guard every check by
-- relation and name so replaying the migration after a lost Goose ledger row
-- is a no-op instead of failing with SQLSTATE 42710.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_delivery_version_positive'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_delivery_version_positive
            CHECK (delivery_version >= 1);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_delivered_version_range'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_delivered_version_range
            CHECK (delivered_version IS NULL OR
                   (delivered_version >= 1 AND delivered_version <= delivery_version));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_delivery_status_shape'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_delivery_status_shape
            CHECK (delivery_status IN ('pending', 'delivered', 'failed'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_secrets_delivery_state_consistent'
          AND conrelid = 'app_secrets'::regclass
    ) THEN
        ALTER TABLE app_secrets
            ADD CONSTRAINT app_secrets_delivery_state_consistent
            CHECK (
                (delivery_status = 'delivered' AND
                 delivered_version = delivery_version AND
                 last_delivery_attempt_at IS NOT NULL AND
                 last_delivered_at IS NOT NULL AND
                 last_delivery_error_code IS NULL AND
                 last_delivered_wake_id IS NOT NULL AND
                 last_delivered_instance_id IS NOT NULL)
                OR
                (delivery_status = 'failed' AND
                 last_delivery_attempt_at IS NOT NULL AND
                 last_delivery_error_code IS NOT NULL AND
                 (delivered_version IS NULL OR delivered_version < delivery_version))
                OR
                (delivery_status = 'pending' AND
                 last_delivery_error_code IS NULL AND
                 (delivered_version IS NULL OR delivered_version < delivery_version))
            );
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_secrets
    DROP CONSTRAINT IF EXISTS app_secrets_delivery_state_consistent,
    DROP CONSTRAINT IF EXISTS app_secrets_delivery_status_shape,
    DROP CONSTRAINT IF EXISTS app_secrets_delivered_version_range,
    DROP CONSTRAINT IF EXISTS app_secrets_delivery_version_positive,
    DROP COLUMN IF EXISTS last_delivered_instance_id,
    DROP COLUMN IF EXISTS last_delivered_wake_id,
    DROP COLUMN IF EXISTS last_delivery_error_code,
    DROP COLUMN IF EXISTS last_delivered_at,
    DROP COLUMN IF EXISTS last_delivery_attempt_at,
    DROP COLUMN IF EXISTS delivery_status,
    DROP COLUMN IF EXISTS delivered_version,
    DROP COLUMN IF EXISTS delivery_version;
-- +goose StatementEnd
