-- +goose Up
CREATE TABLE IF NOT EXISTS dev_bridge_webhook_replays (
    id uuid PRIMARY KEY,
    session_id text NOT NULL REFERENCES dev_bridge_sessions(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    invocation_id uuid NOT NULL,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 64),
    state text NOT NULL DEFAULT 'dispatching' CHECK (state IN ('dispatching','completed','uncertain')),
    http_status integer NOT NULL DEFAULT 0 CHECK (http_status = 0 OR http_status BETWEEN 100 AND 599),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (session_id, idempotency_key)
);

-- +goose Down
DROP TABLE dev_bridge_webhook_replays;
