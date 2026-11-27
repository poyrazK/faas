-- filename: 20261121140659101_execution_admissions.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Atomic source Run admission identity; source and input stay sealed.
CREATE TABLE IF NOT EXISTS execution_admissions (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    scope_hash bytea NOT NULL CHECK (octet_length(scope_hash) = 32),
    key_hash bytea NOT NULL CHECK (octet_length(key_hash) = 32),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    execution_id uuid NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    PRIMARY KEY (account_id, scope_hash, key_hash)
);
CREATE INDEX IF NOT EXISTS execution_admissions_expiry_idx ON execution_admissions (expires_at);
CREATE INDEX IF NOT EXISTS execution_admissions_execution_idx ON execution_admissions (execution_id);
COMMENT ON TABLE execution_admissions IS
    'Atomic source Run request identity, committed with quota, grants, payload and receipt; ADR-20261113010000.';
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS execution_admissions;
