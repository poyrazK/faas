-- ADR-20261127191645: the tool contract gate for sold MCP servers.

-- name: GetMCPContractBaseline :one
-- The effective baseline: the newest accepted candidate contract since the
-- listing was (re)published, else the contract the listing was published with.
SELECT l.account_id, l.version AS listing_version, l.endpoint, l.probe_secret, l.published_at,
       COALESCE(c.candidate, l.contract)::jsonb AS contract,
       COALESCE(c.candidate_sha256, l.contract_sha256)::text AS contract_sha256,
       COALESCE(c.deployment_id::text, '')::text AS from_deployment
  FROM mcp_listings l
  LEFT JOIN LATERAL (
       SELECT k.candidate, k.candidate_sha256, k.deployment_id FROM mcp_contract_checks k
        WHERE k.app_id = l.app_id AND k.account_id = l.account_id AND k.accepted_at IS NOT NULL
          AND k.accepted_at >= l.published_at AND k.candidate IS NOT NULL
        ORDER BY k.accepted_at DESC LIMIT 1) c ON true
 WHERE l.app_id = $1 AND l.contract IS NOT NULL;

-- name: GetMCPContractCheck :one
SELECT * FROM mcp_contract_checks WHERE deployment_id = $1;

-- name: ClaimMCPContractOverride :one
-- Consumes the oldest open override of the app for one candidate.
UPDATE mcp_contract_overrides SET used_by_deployment = sqlc.arg(deployment_id), used_at = now()
 WHERE id = (SELECT o.id FROM mcp_contract_overrides o
              WHERE o.app_id = sqlc.arg(app_id) AND o.account_id = sqlc.arg(account_id)
                AND o.used_at IS NULL AND o.expires_at > now()
              ORDER BY o.created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING *;

-- name: UpsertMCPContractCheck :one
INSERT INTO mcp_contract_checks (deployment_id, app_id, account_id, baseline_sha256, candidate_sha256, candidate, verdict, changes, override_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (deployment_id) DO UPDATE SET
    baseline_sha256 = excluded.baseline_sha256, candidate_sha256 = excluded.candidate_sha256,
    candidate = excluded.candidate, verdict = excluded.verdict, changes = excluded.changes,
    override_id = COALESCE(excluded.override_id, mcp_contract_checks.override_id), checked_at = now()
WHERE mcp_contract_checks.accepted_at IS NULL AND mcp_contract_checks.app_id = excluded.app_id
RETURNING *;

-- name: CreateMCPContractOverride :one
-- At most sqlc.arg(open_cap) open overrides per app.
INSERT INTO mcp_contract_overrides (app_id, account_id, reason, created_by, expires_at)
SELECT sqlc.arg(app_id), sqlc.arg(account_id), sqlc.arg(reason), sqlc.arg(created_by), sqlc.arg(expires_at)
 WHERE (SELECT count(*) FROM mcp_contract_overrides o
         WHERE o.app_id = sqlc.arg(app_id) AND o.used_at IS NULL AND o.expires_at > now()) < sqlc.arg(open_cap)::bigint
RETURNING *;

-- name: ListOpenMCPContractOverrides :many
SELECT * FROM mcp_contract_overrides
 WHERE account_id = $1 AND app_id = $2 AND used_at IS NULL AND expires_at > now()
 ORDER BY created_at;

-- name: ListMCPContractChecks :many
SELECT * FROM mcp_contract_checks WHERE account_id = $1 AND app_id = $2
 ORDER BY checked_at DESC LIMIT $3;

-- name: ListMCPContractChangelog :many
-- Accepted changes since the listing was (re)published, newest first.
SELECT k.* FROM mcp_contract_checks k
  JOIN mcp_listings l ON l.app_id = k.app_id AND l.account_id = k.account_id
 WHERE k.account_id = $1 AND k.app_id = $2 AND k.accepted_at IS NOT NULL
   AND k.accepted_at >= l.published_at AND k.verdict <> 'unchanged'
 ORDER BY k.accepted_at DESC LIMIT $3;
