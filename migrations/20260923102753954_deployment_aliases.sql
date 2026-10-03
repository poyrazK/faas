-- filename: 20260923102753954_deployment_aliases.sql

-- +goose Up
-- +goose StatementBegin
-- Customer-managed names are pointers to immutable deployment revisions.
-- They do not participate in the app's production traffic weights. The
-- deployment target must belong to app_id; the write query enforces that
-- relation atomically while the FKs preserve both rows' lifetimes.
CREATE TABLE IF NOT EXISTS deployment_aliases (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deployment_aliases_pkey PRIMARY KEY (app_id, name),
    CONSTRAINT deployment_aliases_name_format_chk
        CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$')
);

CREATE INDEX IF NOT EXISTS deployment_aliases_deployment_idx
    ON deployment_aliases (deployment_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS deployment_aliases;
-- +goose StatementEnd
