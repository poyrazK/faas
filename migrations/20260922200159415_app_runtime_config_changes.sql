-- filename: 20260922200159415_app_runtime_config_changes.sql

-- +goose Up
-- +goose StatementBegin
-- Issue #3360: the last time an app's secrets or environment changed. apid
-- stamps it on every runtime-config mutation, alongside snapshot
-- invalidation. schedd refuses to snapshot an instance whose process started
-- before the stamp: capturing it on idle park would publish a fresh snapshot
-- of the previous environment and the next wake would restore it.
CREATE TABLE IF NOT EXISTS app_runtime_config_changes (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    changed_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_runtime_config_changes;
-- +goose StatementEnd
