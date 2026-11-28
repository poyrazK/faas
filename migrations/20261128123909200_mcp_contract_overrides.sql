-- filename: 20261128123909200_mcp_contract_overrides.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- One-shot, audited permission for one breaking or unverified candidate.
-- apid writes it; imaged consumes it while recording the check.
CREATE TABLE IF NOT EXISTS mcp_contract_overrides (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id             uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id         uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    reason             text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 512),
    created_by         text NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 200),
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    -- Set by gregale mcp server deploy --allow-breaking: only that
    -- deployment may use it. NULL: the app's next breaking candidate.
    deployment_id      uuid REFERENCES deployments(id) ON DELETE CASCADE,
    used_by_deployment uuid,
    used_at            timestamptz,
    CHECK (expires_at > created_at),
    CHECK ((used_by_deployment IS NULL) = (used_at IS NULL))
);
CREATE INDEX IF NOT EXISTS mcp_contract_overrides_open_idx
    ON mcp_contract_overrides (app_id, created_at) WHERE used_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS mcp_contract_overrides;
-- +goose StatementEnd
