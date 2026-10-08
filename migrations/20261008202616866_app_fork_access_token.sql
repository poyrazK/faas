-- filename: 20261008202616866_app_fork_access_token.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-732 access: apid mints a random per-fork access token, returns it once
-- at creation, and stores only its SHA-256. The gateway routes a request
-- that carries the fork id and token to that fork's instance. Rows created
-- before this column existed have no token and cannot be reached.
ALTER TABLE app_forks
    ADD COLUMN IF NOT EXISTS access_token_hash bytea;

ALTER TABLE app_forks
    DROP CONSTRAINT IF EXISTS app_forks_access_token_hash_chk;

ALTER TABLE app_forks
    ADD CONSTRAINT app_forks_access_token_hash_chk
    CHECK (access_token_hash IS NULL OR octet_length(access_token_hash) = 32);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_forks
    DROP CONSTRAINT IF EXISTS app_forks_access_token_hash_chk;
ALTER TABLE app_forks
    DROP COLUMN IF EXISTS access_token_hash;
-- +goose StatementEnd
