-- +goose Up
-- +goose StatementBegin

ALTER TABLE upload_sessions
    ADD COLUMN IF NOT EXISTS deploy_options jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE upload_sessions DROP COLUMN IF EXISTS deploy_options;

-- +goose StatementEnd
