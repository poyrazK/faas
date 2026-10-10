-- filename: 20261008195258381_dev_source_patches.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-740 phase 2. apid records the full source manifest of every developer
-- deployment so a live patch is always the cumulative difference between the
-- live build's source and the latest synced source, never a chain of deltas.
CREATE TABLE IF NOT EXISTS dev_source_manifests (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id        uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    source_root   text NOT NULL DEFAULT '' CHECK (octet_length(source_root) <= 512),
    manifest      jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'object' AND octet_length(manifest::text) <= 4194304),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS dev_source_manifests_app_created_idx
    ON dev_source_manifests (app_id, created_at DESC);

-- Each row is a complete patch against base_deployment_id's source: the
-- changed files as a bounded tar.gz plus the deleted paths. Generations
-- increase per base deployment; vmmd serves only the newest unexpired one.
CREATE TABLE IF NOT EXISTS dev_source_patches (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id             uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    base_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    generation         bigint NOT NULL CHECK (generation > 0),
    image_dir          text NOT NULL CHECK (image_dir = '/app'),
    archive            bytea NOT NULL CHECK (octet_length(archive) <= 16777216),
    deleted            jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(deleted) = 'array'),
    digest             text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL CHECK (expires_at > created_at),
    UNIQUE (base_deployment_id, generation)
);
CREATE INDEX IF NOT EXISTS dev_source_patches_app_idx ON dev_source_patches (app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dev_source_patches;
DROP TABLE IF EXISTS dev_source_manifests;
-- +goose StatementEnd
