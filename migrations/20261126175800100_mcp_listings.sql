-- filename: 20261126175800100_mcp_listings.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261126175800: a sold MCP server's public listing. One row per app
-- while the seller has it published; unpublishing deletes the row. apid
-- writes it; tools is the snapshot the seller's CLI took when publishing.
CREATE TABLE IF NOT EXISTS mcp_listings (
    app_id        uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    title         text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    summary       text NOT NULL DEFAULT '' CHECK (char_length(summary) <= 500),
    endpoint      text NOT NULL CHECK (endpoint LIKE 'https://%' AND char_length(endpoint) <= 2048),
    tools         jsonb NOT NULL DEFAULT '[]'::jsonb
                  CHECK (jsonb_typeof(tools) = 'array' AND octet_length(tools::text) <= 262144),
    registry_name text NOT NULL DEFAULT ''
                  CHECK (registry_name = '' OR registry_name ~ '^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$'),
    show_usage    boolean NOT NULL DEFAULT false,
    version       integer NOT NULL DEFAULT 1 CHECK (version > 0),
    published_at  timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS mcp_listings;
-- +goose StatementEnd
