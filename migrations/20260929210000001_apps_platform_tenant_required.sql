-- +goose Up
-- Platform operators may require verified end-customer identity on app traffic.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS platform_tenant_required boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE apps DROP COLUMN IF EXISTS platform_tenant_required;
