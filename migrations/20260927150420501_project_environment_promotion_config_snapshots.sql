-- filename: 20260927150420501_project_environment_promotion_config_snapshots.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    ADD COLUMN IF NOT EXISTS sync_config boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS source_config_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS previous_target_config_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_config_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS previous_target_config_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS target_config_version bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS rollback_config_version bigint NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    DROP COLUMN IF EXISTS rollback_config_version,
    DROP COLUMN IF EXISTS target_config_version,
    DROP COLUMN IF EXISTS previous_target_config_snapshot,
    DROP COLUMN IF EXISTS source_config_snapshot,
    DROP COLUMN IF EXISTS previous_target_config_hash,
    DROP COLUMN IF EXISTS source_config_hash,
    DROP COLUMN IF EXISTS sync_config;
-- +goose StatementEnd
