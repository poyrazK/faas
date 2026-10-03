-- +goose Up
-- +goose StatementBegin
-- An independent, capped inventory. Request payloads and query strings never
-- enter this table. The receipt flag lives on the already-idempotent financial
-- event so a replay cannot increment the route count twice.
ALTER TABLE api_consumer_usage_events
    ADD COLUMN IF NOT EXISTS discovery_recorded boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS app_api_routes (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    route_template text NOT NULL CHECK (length(route_template) BETWEEN 1 AND 256),
    first_seen timestamptz NOT NULL,
    last_seen timestamptz NOT NULL,
    request_count bigint NOT NULL CHECK (request_count > 0),
    PRIMARY KEY (account_id, app_id, route_template),
    CHECK (last_seen >= first_seen)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_api_routes;
ALTER TABLE api_consumer_usage_events DROP COLUMN IF EXISTS discovery_recorded;
-- +goose StatementEnd
