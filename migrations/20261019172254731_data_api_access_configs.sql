-- filename: 20261019172254731_data_api_access_configs.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks apps briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261016151947: the access settings a Data API runtime last reported
-- (exposed schemas, public-read allowlist, server-key hashes and permissions),
-- which realtime endpoints of the same app apply to live reads without a user
-- token. Hashes only; no key is ever stored.
CREATE TABLE IF NOT EXISTS data_api_access_configs (
    app_id      uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    config      jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 32768),
    reported_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE data_api_access_configs IS
    'ADR-20261016151947 Data API access settings last reported by the runtime, applied to anonymous and server-key live reads.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_api_access_configs;
-- +goose StatementEnd
