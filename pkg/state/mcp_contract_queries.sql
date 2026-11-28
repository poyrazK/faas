-- ADR-20261127191645: the tool contract gate for sold MCP servers.

-- name: GetMCPContractBaseline :one
-- The effective baseline: the contract of the deployment that most recently
-- went live (rollbacks included) since the listing was (re)published. When
-- there is none, or its tools are not known (unverified or unknown), it is
-- the contract the listing was published with.
SELECT l.account_id, l.version AS listing_version, l.endpoint, l.probe_secret, l.published_at,
       COALESCE(c.candidate, l.contract)::jsonb AS contract,
       COALESCE(c.candidate_sha256, l.contract_sha256)::text AS contract_sha256,
       (CASE WHEN c.candidate IS NULL THEN '' ELSE c.deployment_id::text END)::text AS from_deployment,
       -- ADR-20261128135329: the scan of the same source, and whether its
       -- high findings were reviewed.
       (CASE WHEN c.candidate IS NULL THEN l.tool_scan ELSE c.tool_scan END)::jsonb AS tool_scan,
       (CASE WHEN c.candidate IS NULL THEN l.tool_scan_at ELSE c.checked_at END)::timestamptz AS tool_scan_at,
       (CASE WHEN c.candidate IS NULL THEN l.tool_scan_review <> '' ELSE c.tool_scan_reviewed END)::boolean AS tool_scan_reviewed
  FROM mcp_listings l
  LEFT JOIN LATERAL (
       SELECT k.candidate, k.candidate_sha256, k.deployment_id, k.tool_scan, k.checked_at, k.tool_scan_reviewed
         FROM mcp_contract_checks k
        WHERE k.app_id = l.app_id AND k.account_id = l.account_id AND k.live_at IS NOT NULL
          AND k.live_at >= l.published_at
        ORDER BY k.live_at DESC LIMIT 1) c ON true
 WHERE l.app_id = $1 AND l.contract IS NOT NULL;

-- name: GetMCPContractCheck :one
SELECT * FROM mcp_contract_checks WHERE deployment_id = $1;

-- name: ClaimMCPContractOverride :one
-- Consumes an open override of the app of one kind for one candidate: one
-- bound to this deployment first, else the oldest unbound one.
UPDATE mcp_contract_overrides SET used_by_deployment = sqlc.arg(deployment_id), used_at = now()
 WHERE id = (SELECT o.id FROM mcp_contract_overrides o
              WHERE o.app_id = sqlc.arg(app_id) AND o.account_id = sqlc.arg(account_id)
                AND o.kind = sqlc.arg(kind)
                AND o.used_at IS NULL AND o.expires_at > now()
                AND (o.deployment_id = sqlc.arg(deployment_id)
                     OR o.deployment_id IS NULL AND sqlc.arg(allow_unbound)::boolean)
              ORDER BY (o.deployment_id IS NULL), o.created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING *;

-- name: UpsertMCPContractCheck :one
INSERT INTO mcp_contract_checks (deployment_id, app_id, account_id, baseline_sha256, candidate_sha256, candidate, verdict, changes, override_id,
    tool_scan, tool_scan_high, tool_scan_new_high, tool_scan_override_id, tool_scan_reviewed)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, sqlc.narg(tool_scan), sqlc.arg(tool_scan_high), sqlc.arg(tool_scan_new_high),
    sqlc.narg(tool_scan_override_id), sqlc.arg(tool_scan_reviewed))
ON CONFLICT (deployment_id) DO UPDATE SET
    baseline_sha256 = excluded.baseline_sha256, candidate_sha256 = excluded.candidate_sha256,
    candidate = excluded.candidate, verdict = excluded.verdict, changes = excluded.changes,
    override_id = COALESCE(excluded.override_id, mcp_contract_checks.override_id),
    tool_scan = excluded.tool_scan, tool_scan_high = excluded.tool_scan_high, tool_scan_new_high = excluded.tool_scan_new_high,
    tool_scan_override_id = COALESCE(excluded.tool_scan_override_id, mcp_contract_checks.tool_scan_override_id),
    tool_scan_reviewed = excluded.tool_scan_reviewed, checked_at = now()
WHERE mcp_contract_checks.accepted_at IS NULL AND mcp_contract_checks.app_id = excluded.app_id
RETURNING *;

-- name: LockMCPContractOverrides :exec
-- Serializes override creation per app so the open cap holds.
SELECT pg_advisory_xact_lock(hashtextextended('mcp_contract_overrides:' || sqlc.arg(app_id)::uuid::text, 0));

-- name: CreateMCPContractOverride :one
-- At most sqlc.arg(open_cap) open overrides per app; run under
-- LockMCPContractOverrides.
INSERT INTO mcp_contract_overrides (app_id, account_id, reason, created_by, expires_at, deployment_id, kind)
SELECT sqlc.arg(app_id), sqlc.arg(account_id), sqlc.arg(reason), sqlc.arg(created_by), sqlc.arg(expires_at), sqlc.narg(deployment_id), sqlc.arg(kind)
 WHERE (SELECT count(*) FROM mcp_contract_overrides o
           LEFT JOIN deployments d ON d.id = o.deployment_id
         WHERE o.app_id = sqlc.arg(app_id) AND o.used_at IS NULL AND o.expires_at > now()
           AND (o.deployment_id IS NULL OR d.status IN ('pending', 'building', 'imaging', 'snapshotting'))) < sqlc.arg(open_cap)::bigint
RETURNING *;

-- name: ListOpenMCPContractOverrides :many
-- Usable overrides: unused, unexpired, and unbound or bound to a deployment
-- that is still a candidate.
SELECT o.* FROM mcp_contract_overrides o
  LEFT JOIN deployments d ON d.id = o.deployment_id
 WHERE o.account_id = $1 AND o.app_id = $2 AND o.used_at IS NULL AND o.expires_at > now()
   AND (o.deployment_id IS NULL OR d.status IN ('pending', 'building', 'imaging', 'snapshotting'))
 ORDER BY o.created_at;

-- name: ListMCPContractChecks :many
SELECT * FROM mcp_contract_checks WHERE account_id = $1 AND app_id = $2
 ORDER BY checked_at DESC LIMIT $3;

-- name: ListMCPContractChangelog :many
-- Accepted changes since the listing was (re)published, newest first.
SELECT k.* FROM mcp_contract_checks k
  JOIN mcp_listings l ON l.app_id = k.app_id AND l.account_id = k.account_id
 WHERE k.account_id = $1 AND k.app_id = $2 AND k.accepted_at IS NOT NULL
   AND k.accepted_at >= l.published_at AND k.verdict NOT IN ('unchanged', 'unknown')
 ORDER BY k.accepted_at DESC LIMIT $3;
