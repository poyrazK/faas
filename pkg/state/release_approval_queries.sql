-- ADR-20261020085706: release approvals.

-- name: UpsertAppReleaseApprovalPolicy :one
INSERT INTO app_release_approval_policies (
  app_id, account_id, required_approvals, after_stage, timeout_minutes, approvers,
  allow_self_approval, notify_channel_ids, updated_by, created_at, updated_at
)
VALUES (
  sqlc.arg(app_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(required_approvals)::smallint,
  sqlc.arg(after_stage)::smallint, sqlc.arg(timeout_minutes)::integer, sqlc.arg(approvers)::text,
  sqlc.arg(allow_self_approval)::boolean, sqlc.arg(notify_channel_ids)::uuid[], sqlc.arg(updated_by)::text,
  sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz
)
ON CONFLICT (app_id) DO UPDATE SET
  required_approvals = EXCLUDED.required_approvals,
  after_stage = EXCLUDED.after_stage,
  timeout_minutes = EXCLUDED.timeout_minutes,
  approvers = EXCLUDED.approvers,
  allow_self_approval = EXCLUDED.allow_self_approval,
  notify_channel_ids = EXCLUDED.notify_channel_ids,
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

-- name: EnqueueReleaseApprovalEvent :one
-- Records an event once per release and kind; a repeat returns no row.
INSERT INTO release_approval_events (
  id, account_id, app_id, deployment_id, event, channel_ids, payload, next_attempt_at, created_at
)
VALUES (
  sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(deployment_id)::uuid,
  sqlc.arg(event)::text, sqlc.arg(channel_ids)::uuid[], sqlc.arg(payload)::jsonb, sqlc.arg(now)::timestamptz,
  sqlc.arg(now)::timestamptz
)
ON CONFLICT (deployment_id, event) DO NOTHING
RETURNING id;

-- name: ListDueReleaseApprovalEvents :many
SELECT * FROM release_approval_events
 WHERE state = 'pending' AND next_attempt_at <= sqlc.arg(now)::timestamptz
 ORDER BY next_attempt_at, id
 LIMIT sqlc.arg(max_events)::integer;

-- name: FinishReleaseApprovalEvent :execrows
-- Records one delivery attempt of a pending event.
UPDATE release_approval_events
   SET state = sqlc.arg(state)::text,
       attempts = attempts + 1,
       last_error = sqlc.arg(last_error)::text,
       next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz,
       delivered_at = sqlc.narg(delivered_at)::timestamptz
 WHERE id = sqlc.arg(id)::uuid AND state = 'pending';
