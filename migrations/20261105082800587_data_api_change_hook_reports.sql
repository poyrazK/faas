-- filename: 20261105082800587_data_api_change_hook_reports.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks apps briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261022121642 amendment 1: the change hook delivery a Data API runtime
-- last reported from its change event table, so owners see waiting and
-- rejected changes without waking the API. Counts and codes only; no change
-- data is stored.
CREATE TABLE IF NOT EXISTS data_api_change_hook_reports (
    app_id            uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    reported_at       timestamptz NOT NULL,
    pending           bigint NOT NULL CHECK (pending BETWEEN 0 AND 1000000000000),
    failed            bigint NOT NULL CHECK (failed BETWEEN 0 AND 1000000000000),
    oldest_pending_at timestamptz,
    last_published_at timestamptz,
    last_failure_code text CHECK (last_failure_code ~ '^[a-z0-9_]{1,64}$'),
    publishing        text NOT NULL CHECK (publishing IN ('ok', 'retrying')),
    CHECK ((pending = 0) = (oldest_pending_at IS NULL))
);
COMMENT ON TABLE data_api_change_hook_reports IS
    'ADR-20261022121642 amendment 1: change hook delivery last reported by each Data API runtime (counts and codes only).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_api_change_hook_reports;
-- +goose StatementEnd
