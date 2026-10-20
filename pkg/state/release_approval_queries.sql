-- ADR-20261020085706: release approvals.

-- name: UpsertAppReleaseApprovalPolicy :one
INSERT INTO app_release_approval_policies (
  app_id, account_id, required_approvals, after_stage, timeout_minutes, approvers,
  allow_self_approval, updated_by, created_at, updated_at
)
VALUES (
  sqlc.arg(app_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(required_approvals)::smallint,
  sqlc.arg(after_stage)::smallint, sqlc.arg(timeout_minutes)::integer, sqlc.arg(approvers)::text,
  sqlc.arg(allow_self_approval)::boolean, sqlc.arg(updated_by)::text, sqlc.arg(now)::timestamptz,
  sqlc.arg(now)::timestamptz
)
ON CONFLICT (app_id) DO UPDATE SET
  required_approvals = EXCLUDED.required_approvals,
  after_stage = EXCLUDED.after_stage,
  timeout_minutes = EXCLUDED.timeout_minutes,
  approvers = EXCLUDED.approvers,
  allow_self_approval = EXCLUDED.allow_self_approval,
  updated_by = EXCLUDED.updated_by,
  updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetAppReleaseApprovalPolicy :one
SELECT * FROM app_release_approval_policies WHERE app_id = sqlc.arg(app_id)::uuid;

-- name: DeleteAppReleaseApprovalPolicy :one
DELETE FROM app_release_approval_policies WHERE app_id = sqlc.arg(app_id)::uuid
RETURNING *;

-- name: CreateDeploymentReleaseApproval :one
-- Records one person's decision on a release; a second decision by the same
-- person returns no row.
INSERT INTO deployment_release_approvals (
  id, account_id, app_id, deployment_id, approver_account_id, approver, approver_role,
  decision, comment, created_at
)
VALUES (
  sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid,
  sqlc.arg(deployment_id)::uuid, sqlc.arg(approver_account_id)::uuid, sqlc.arg(approver)::text,
  sqlc.arg(approver_role)::text, sqlc.arg(decision)::text, sqlc.arg(comment)::text,
  sqlc.arg(now)::timestamptz
)
ON CONFLICT (deployment_id, approver_account_id) DO NOTHING
RETURNING *;

-- name: ListDeploymentReleaseApprovals :many
SELECT * FROM deployment_release_approvals
 WHERE deployment_id = sqlc.arg(deployment_id)::uuid
 ORDER BY created_at, id;
