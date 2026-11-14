-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
-- ADR-20261107220000: one row per write request an agent key made, kept 7
-- days. apid inserts it after the handler answers and sweeps old rows.
CREATE TABLE IF NOT EXISTS agent_activity (
    id          uuid PRIMARY KEY,
    key_id      uuid NOT NULL,
    account_id  uuid NOT NULL,
    org_id      uuid,
    method      text NOT NULL CHECK (method IN ('POST', 'PUT', 'PATCH', 'DELETE')),
    path        text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 512),
    status      integer NOT NULL CHECK (status BETWEEN 100 AND 599),
    approval_id uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS agent_activity_key_created_idx
    ON agent_activity (key_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS agent_activity_created_idx
    ON agent_activity (created_at);

-- +goose Down
DROP TABLE IF EXISTS agent_activity;
