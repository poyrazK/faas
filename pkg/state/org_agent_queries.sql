-- name: GetOrgAgentPolicy :one
-- ADR-20261101225122: a shared organization's agent policy.
SELECT org_id, agents_allowed, allowed_scopes, updated_at, updated_by, approval_categories, max_agent_budget_cents
FROM org_agent_policies
WHERE org_id = sqlc.arg(org_id)::uuid;

-- name: UpsertOrgAgentPolicy :one
INSERT INTO org_agent_policies (org_id, agents_allowed, allowed_scopes, approval_categories, max_agent_budget_cents, updated_at, updated_by)
VALUES (sqlc.arg(org_id)::uuid, sqlc.arg(agents_allowed)::boolean, sqlc.arg(allowed_scopes)::text[], sqlc.arg(approval_categories)::text[],
        sqlc.narg(max_agent_budget_cents)::bigint, now(), sqlc.narg(updated_by)::uuid)
ON CONFLICT (org_id) DO UPDATE
SET agents_allowed = EXCLUDED.agents_allowed,
    allowed_scopes = EXCLUDED.allowed_scopes,
    approval_categories = EXCLUDED.approval_categories,
    max_agent_budget_cents = EXCLUDED.max_agent_budget_cents,
    updated_at = now(),
    updated_by = EXCLUDED.updated_by
RETURNING org_id, agents_allowed, allowed_scopes, updated_at, updated_by, approval_categories, max_agent_budget_cents;

-- name: SetCliAuthCodeOrg :execrows
-- Binds a code still waiting for approval to the organization chosen for it.
UPDATE cli_auth_codes
SET org_id = sqlc.narg(org_id)::uuid
WHERE token_hash = sqlc.arg(token_hash)
  AND status = 'pending'
  AND expires_at > now();

-- name: GetCliAuthCodeOrg :one
SELECT org_id FROM cli_auth_codes WHERE token_hash = sqlc.arg(token_hash);
