-- ADR-20261126175800: a sold MCP server's public listing.

-- name: GetMCPListing :one
SELECT * FROM mcp_listings WHERE account_id = $1 AND app_id = $2;

-- name: PutMCPListing :one
INSERT INTO mcp_listings (app_id, account_id, title, summary, endpoint, tools, registry_name, show_usage)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (app_id) DO UPDATE SET
    title = excluded.title, summary = excluded.summary, endpoint = excluded.endpoint, tools = excluded.tools,
    registry_name = excluded.registry_name, show_usage = excluded.show_usage,
    version = mcp_listings.version + 1, published_at = now(), updated_at = now()
WHERE mcp_listings.account_id = excluded.account_id
RETURNING *;

-- name: DeleteMCPListing :execrows
DELETE FROM mcp_listings WHERE account_id = $1 AND app_id = $2;
