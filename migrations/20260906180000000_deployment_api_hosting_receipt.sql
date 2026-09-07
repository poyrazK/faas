-- filename: 20260906180000000_deployment_api_hosting_receipt.sql
-- +goose Up
-- +goose StatementBegin

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS api_hosting_receipt jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE deployments DROP COLUMN IF EXISTS api_hosting_receipt;

-- +goose StatementEnd
