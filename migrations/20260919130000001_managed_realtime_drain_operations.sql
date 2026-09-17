-- Durable history for bounded managed realtime connection drains.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_realtime_drain_operations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('running', 'completed', 'partial')),
    reason text NOT NULL,
    dry_run boolean NOT NULL DEFAULT false,
    matched integer NOT NULL DEFAULT 0 CHECK (matched >= 0),
    closed integer NOT NULL DEFAULT 0 CHECK (closed >= 0),
    gone integer NOT NULL DEFAULT 0 CHECK (gone >= 0),
    failed integer NOT NULL DEFAULT 0 CHECK (failed >= 0),
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS managed_realtime_drain_operations_scope_idx
    ON managed_realtime_drain_operations (account_id, endpoint_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS managed_realtime_drain_operations_scope_idx;
DROP TABLE IF EXISTS managed_realtime_drain_operations;
-- +goose StatementEnd
