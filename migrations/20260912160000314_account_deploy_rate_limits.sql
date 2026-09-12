-- +goose Up
CREATE TABLE IF NOT EXISTS account_deploy_rate_limits (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    deploys_used INTEGER NOT NULL DEFAULT 0 CHECK (deploys_used >= 0),
    deploys_window_start TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS account_deploy_rate_limits;
