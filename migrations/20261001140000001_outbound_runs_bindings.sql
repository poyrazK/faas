-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS runs_enabled boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE outbound_integrations DROP COLUMN IF EXISTS runs_enabled;
