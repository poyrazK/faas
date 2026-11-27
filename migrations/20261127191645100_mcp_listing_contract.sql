-- filename: 20261127191645100_mcp_listing_contract.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261127191645: the tool contract a sold MCP server promised. The
-- listing keeps the format-2 catalog contract the seller's CLI captured when
-- publishing (NULL for listings published before the gate) and the app
-- secret imaged sends as the bearer token when it lists a candidate's tools.
ALTER TABLE mcp_listings
    ADD COLUMN IF NOT EXISTS contract jsonb,
    ADD COLUMN IF NOT EXISTS contract_sha256 text,
    ADD COLUMN IF NOT EXISTS probe_secret text NOT NULL DEFAULT '';
ALTER TABLE mcp_listings ADD CONSTRAINT mcp_listings_contract_chk CHECK (
    (contract IS NULL AND contract_sha256 IS NULL)
    OR (jsonb_typeof(contract) = 'object' AND octet_length(contract::text) <= 1048576
        AND contract_sha256 ~ '^[0-9a-f]{64}$')) NOT VALID;
ALTER TABLE mcp_listings ADD CONSTRAINT mcp_listings_probe_secret_chk CHECK (
    probe_secret = '' OR probe_secret ~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$') NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE mcp_listings DROP CONSTRAINT IF EXISTS mcp_listings_probe_secret_chk;
ALTER TABLE mcp_listings DROP CONSTRAINT IF EXISTS mcp_listings_contract_chk;
ALTER TABLE mcp_listings DROP COLUMN IF EXISTS probe_secret;
ALTER TABLE mcp_listings DROP COLUMN IF EXISTS contract_sha256;
ALTER TABLE mcp_listings DROP COLUMN IF EXISTS contract;
-- +goose StatementEnd
