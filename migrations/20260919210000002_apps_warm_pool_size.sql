-- filename: 20260919210000002_apps_warm_pool_size.sql

-- +goose Up
-- +goose StatementBegin

ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS warm_pool_size integer NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conname = 'apps_warm_pool_size_chk'
           AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_warm_pool_size_chk
            CHECK (warm_pool_size >= 0 AND warm_pool_size <= max_concurrency);
    END IF;
END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_warm_pool_size_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS warm_pool_size;

-- +goose StatementEnd
