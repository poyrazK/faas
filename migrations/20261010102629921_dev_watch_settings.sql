-- filename: 20261010102629921_dev_watch_settings.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-970. apid records a developer environment's watch-mode command when
-- the CLI upserts the session; builderd reads it for developer builds. A
-- missing row means watch mode is off.
CREATE TABLE IF NOT EXISTS dev_watch_settings (
    app_id     uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    command    text NOT NULL CHECK (octet_length(command) BETWEEN 1 AND 512 AND command !~ '[\n\r]'),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dev_watch_settings;
-- +goose StatementEnd
