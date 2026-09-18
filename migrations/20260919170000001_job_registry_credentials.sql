-- filename: 20260919170000001_job_registry_credentials.sql
-- +goose Up
-- +goose StatementBegin

-- Jobs are account-owned rather than app-owned, so private OCI credentials
-- need their own scope. Passwords are age-sealed by apid with namespace
-- "registry_creds" and are only opened transiently by imaged.
CREATE TABLE IF NOT EXISTS job_registry_credentials (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    job_id             uuid        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    registry           text        NOT NULL,
    username           text        NOT NULL,
    password_encrypted bytea       NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_used_at       timestamptz,
    CONSTRAINT job_registry_credentials_registry_chk
        CHECK (length(registry) > 0 AND length(registry) <= 253),
    CONSTRAINT job_registry_credentials_username_chk
        CHECK (length(username) > 0 AND length(username) <= 256),
    CONSTRAINT job_registry_credentials_password_chk
        CHECK (length(password_encrypted) > 0),
    CONSTRAINT job_registry_credentials_job_registry_uq
        UNIQUE (job_id, registry)
);

CREATE INDEX IF NOT EXISTS job_registry_credentials_account_idx
    ON job_registry_credentials (account_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS job_registry_credentials;
-- +goose StatementEnd
