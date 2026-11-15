-- filename: 20261115160001100_environment_api_routes.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000: a stage keeps its own discovered-route inventory, so a
-- route first served by a stage deployment fires a first-seen event only to
-- that stage's subscriptions, and production still sees its own first hit.
-- Production keeps app_api_routes unchanged (the previous release writes it
-- ON CONFLICT on its primary key). Deleting the environment deletes its
-- inventory.
CREATE TABLE IF NOT EXISTS environment_api_routes (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    route_template text NOT NULL CHECK (length(route_template) BETWEEN 1 AND 256),
    first_seen timestamptz NOT NULL,
    last_seen timestamptz NOT NULL,
    request_count bigint NOT NULL CHECK (request_count > 0),
    CHECK (last_seen >= first_seen),
    PRIMARY KEY (environment_id, app_id, route_template)
);
CREATE INDEX IF NOT EXISTS environment_api_routes_account_idx ON environment_api_routes (account_id);
CREATE INDEX IF NOT EXISTS environment_api_routes_app_idx ON environment_api_routes (app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS environment_api_routes;
-- +goose StatementEnd
