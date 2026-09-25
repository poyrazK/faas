-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN provider_auth_mode text NOT NULL DEFAULT 'application'
    CONSTRAINT outbound_integrations_provider_auth_mode_chk
        CHECK (provider_auth_mode IN ('application', 'managed'));

-- +goose Down
ALTER TABLE outbound_integrations DROP COLUMN provider_auth_mode;
