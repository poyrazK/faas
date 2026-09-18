-- Persist whether a managed realtime drain explicitly selected all matches.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_realtime_drain_operations
    ADD COLUMN IF NOT EXISTS select_all boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE managed_realtime_drain_operations
    DROP COLUMN IF EXISTS select_all;
-- +goose StatementEnd
