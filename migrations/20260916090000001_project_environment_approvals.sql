-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_environment_approvals (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_slug        text        NOT NULL,
    environment_slug    text        NOT NULL,
    plan_token_hash     text        NOT NULL,
    approval_token_hash text        NOT NULL UNIQUE,
    expires_at          timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_approvals_plan_hash_shape CHECK (plan_token_hash ~ '^[a-f0-9]{64}$'),
    CONSTRAINT project_environment_approvals_token_hash_shape CHECK (approval_token_hash ~ '^[a-f0-9]{64}$'),
    CONSTRAINT project_environment_approvals_project_slug_shape CHECK (project_slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT project_environment_approvals_environment_slug_shape CHECK (environment_slug ~ '^[a-z0-9](?:[a-z0-9-]{0,31}[a-z0-9])?$')
);

CREATE INDEX IF NOT EXISTS project_environment_approvals_lookup_idx
    ON project_environment_approvals (account_id, project_slug, environment_slug, plan_token_hash, expires_at);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS project_environment_approvals;
