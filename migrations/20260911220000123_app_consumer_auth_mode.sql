-- filename: 20260911220000123_app_consumer_auth_mode.sql
-- +goose Up
-- +goose StatementBegin

-- ADR-120: applications opt into end-customer credential
-- authentication independently of the operator API-key surface.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS consumer_auth_mode text NOT NULL DEFAULT 'optional';

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_consumer_auth_mode_chk;
ALTER TABLE apps ADD CONSTRAINT apps_consumer_auth_mode_chk
    CHECK (consumer_auth_mode IN ('optional', 'required'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_consumer_auth_mode_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS consumer_auth_mode;
-- +goose StatementEnd
