-- +goose Up
-- +goose StatementBegin

-- Explicit response-cache purges must survive gateway disconnects. This
-- broadcast ledger is separate from notification_outbox: every gateway
-- independently replays the same ordered changes and publishes its cursor.
CREATE TABLE IF NOT EXISTS response_cache_purge_change_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    app_id      uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    path_glob   text NOT NULL DEFAULT '',
    tag         text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (path_glob = '' OR tag = ''),
    CHECK (octet_length(path_glob) <= 1024),
    CHECK (tag = '' OR tag ~ '^[A-Za-z0-9._:/-]{1,128}$')
);

CREATE INDEX IF NOT EXISTS response_cache_purge_change_log_app_idx
    ON response_cache_purge_change_log (app_id, id);
CREATE INDEX IF NOT EXISTS response_cache_purge_change_log_created_idx
    ON response_cache_purge_change_log (created_at, id);

CREATE TABLE IF NOT EXISTS gateway_response_cache_purge_watermarks (
    node_name       text PRIMARY KEY CHECK (node_name <> ''),
    last_change_id  bigint NOT NULL CHECK (last_change_id >= 0),
    observed_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gateway_response_cache_purge_watermarks;
DROP TABLE IF EXISTS response_cache_purge_change_log;
-- +goose StatementEnd
