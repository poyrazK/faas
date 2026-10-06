-- +goose Up
ALTER TABLE inbound_webhook_endpoints
    DROP CONSTRAINT inbound_webhook_endpoints_provider_chk;
ALTER TABLE inbound_webhook_endpoints
    ADD CONSTRAINT inbound_webhook_endpoints_provider_chk
    CHECK (provider IN ('stripe', 'generic'));

-- +goose Down
-- Forward-only: generic endpoints may already be configured and referenced by
-- customer integrations. Disable or remove them before rolling back binaries.
SELECT 1;
