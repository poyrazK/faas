-- name: SetAPIKeyAgentClient :exec
-- ADR-20261030133644: names the agent client of one of the account's keys.
-- The first write wins; another account's key is left alone.
INSERT INTO api_key_agent_clients (key_id, account_id, client_name, client_site)
SELECT k.id, k.account_id, sqlc.arg(client_name)::text, sqlc.arg(client_site)::text
FROM api_keys k
WHERE k.id = sqlc.arg(key_id)::uuid AND k.account_id = sqlc.arg(account_id)::uuid
ON CONFLICT (key_id) DO NOTHING;

-- name: ListAPIKeyAgentClients :many
-- The named agent clients of the account's usable keys.
SELECT c.key_id, c.client_name, c.client_site
FROM api_key_agent_clients c
JOIN api_keys k ON k.id = c.key_id
WHERE c.account_id = sqlc.arg(account_id)::uuid
  AND k.status IN ('active', 'grace')
  AND (k.expires_at IS NULL OR k.expires_at > now())
ORDER BY c.key_id;
