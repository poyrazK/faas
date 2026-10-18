-- filename: 20261013193012345_dev_watch_host.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1170 phase 3. apid records the developer app's public hostname with
-- its watch-mode command. builderd puts it in the watch-mode image, where
-- development servers need it to accept requests for that host. Rows
-- written before this column have no hostname until the next session upsert.
ALTER TABLE dev_watch_settings
    ADD COLUMN IF NOT EXISTS host text NOT NULL DEFAULT ''
    CONSTRAINT dev_watch_settings_host_check CHECK (octet_length(host) <= 253 AND host ~ '^[a-z0-9.-]*$');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE dev_watch_settings DROP COLUMN IF EXISTS host;
-- +goose StatementEnd
