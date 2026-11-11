-- filename: 20261107172523494_data_api_change_hook_drops.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261022121642 amendment 3: when the runtime last saw capture leave a
-- change out at the backlog bound, so owners see that changes went
-- unrecorded. Nullable and without a default: adding it rewrites nothing.
ALTER TABLE data_api_change_hook_reports ADD COLUMN IF NOT EXISTS last_dropped_at timestamptz;
COMMENT ON COLUMN data_api_change_hook_reports.last_dropped_at IS
    'ADR-20261022121642 amendment 3: when capture last left a change out at the backlog bound, as the runtime last reported.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE data_api_change_hook_reports DROP COLUMN IF EXISTS last_dropped_at;
-- +goose StatementEnd
