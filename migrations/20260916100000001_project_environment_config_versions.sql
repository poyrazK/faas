-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_environment_config_versions (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id       uuid        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_slug text        NOT NULL,
    version          bigint      NOT NULL CHECK (version > 0),
    config_hash      text        NOT NULL,
    config_json      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_config_hash_shape CHECK (config_hash ~ '^[a-f0-9]{64}$'),
    CONSTRAINT project_environment_config_environment_slug_shape CHECK (
        environment_slug ~ '^[a-z0-9](?:[a-z0-9-]{0,31}[a-z0-9])?$'
    ),
    CONSTRAINT project_environment_config_version_uniq UNIQUE (project_id, environment_slug, version)
);

CREATE INDEX IF NOT EXISTS project_environment_config_latest_idx
    ON project_environment_config_versions (project_id, environment_slug, version DESC);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS project_environment_config_versions;
