-- name: GetStoredWorkflowForLiveVerification :one
SELECT * FROM stored_workflows WHERE app_id=$1 AND name=$2;

-- name: InsertStoredWorkflowLiveVerification :one
INSERT INTO stored_workflow_live_verifications
(run_id,app_id,name,case_name,workflow_version,published_version,deployment_id,inputs_hash,live_test,idempotency_key,request_fingerprint)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;

-- name: GetStoredWorkflowLiveVerificationByKey :one
SELECT * FROM stored_workflow_live_verifications WHERE app_id=$1 AND name=$2 AND idempotency_key=$3;

-- name: GetStoredWorkflowLiveVerification :one
SELECT * FROM stored_workflow_live_verifications WHERE app_id=$1 AND name=$2 AND run_id=$3;

-- name: ListStoredWorkflowLiveVerifications :many
SELECT * FROM stored_workflow_live_verifications WHERE app_id=$1 AND name=$2
ORDER BY created_at DESC,run_id DESC LIMIT $3;

-- name: StoredWorkflowLiveRunState :one
SELECT status, resume_count, updated_at, cancelled_at FROM workflow_runs WHERE id=$1;

-- name: LockStoredWorkflowLiveRunState :one
SELECT status, resume_count, updated_at, cancelled_at FROM workflow_runs WHERE id=$1 FOR UPDATE;

-- name: StoredWorkflowLiveStep :many
SELECT s.status,s.output,s.attempt FROM workflow_steps s
WHERE s.run_id=sqlc.arg(run_id)::uuid
AND ((sqlc.arg(loop_name)::text='' AND s.step_name=sqlc.arg(step_name)::text AND s.foreach_parent IS NULL AND s.foreach_index IS NULL)
 OR (sqlc.arg(loop_name)::text<>'' AND s.foreach_parent=sqlc.arg(loop_name)::text AND s.foreach_index=sqlc.narg(item_index)::integer))
LIMIT 2;

-- name: SaveStoredWorkflowLiveReport :execrows
UPDATE stored_workflow_live_verifications SET report=$4
WHERE app_id=$1 AND name=$2 AND run_id=$3;

-- name: DeleteStoredWorkflowLiveVerifications :exec
DELETE FROM stored_workflow_live_verifications WHERE app_id=$1 AND name=$2;

-- name: InsertStoredWorkflowLiveSuite :one
INSERT INTO stored_workflow_live_suites(id,app_id,name,workflow_version,published_version,deployment_id,cases,managed)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING RETURNING *;

-- name: GetStoredWorkflowLiveSuite :one
SELECT * FROM stored_workflow_live_suites WHERE app_id=$1 AND name=$2 AND id=$3;

-- name: UpdateStoredWorkflowLiveSuite :one
UPDATE stored_workflow_live_suites SET cases=$4,updated_at=now() WHERE app_id=$1 AND name=$2 AND id=$3 RETURNING *;

-- name: ListStoredWorkflowLiveSuites :many
SELECT * FROM stored_workflow_live_suites WHERE app_id=$1 AND name=$2 ORDER BY created_at DESC,id DESC LIMIT $3;

-- name: PruneStoredWorkflowLiveSuites :exec
DELETE FROM stored_workflow_live_suites AS suite WHERE suite.app_id=sqlc.arg(app_id)::uuid AND suite.name=sqlc.arg(name)::text AND suite.id IN (
 SELECT history.id FROM stored_workflow_live_suites AS history WHERE history.app_id=sqlc.arg(app_id)::uuid AND history.name=sqlc.arg(name)::text
 ORDER BY (EXISTS (SELECT 1 FROM stored_workflow_live_baseline_promotions AS pin WHERE pin.app_id=history.app_id AND pin.name=history.name AND pin.suite_id=history.id AND NOT EXISTS (SELECT 1 FROM stored_workflow_live_baseline_retirements AS retired WHERE retired.app_id=pin.app_id AND retired.name=pin.name AND retired.baseline_name=pin.baseline_name) AND pin.revision=(SELECT max(head.revision) FROM stored_workflow_live_baseline_promotions AS head WHERE head.app_id=pin.app_id AND head.name=pin.name AND head.baseline_name=pin.baseline_name)) OR (history.managed AND history.created_at>sqlc.arg(protected_since)::timestamptz)) DESC,history.created_at DESC,history.id DESC OFFSET sqlc.arg(retained)::integer
);

-- name: DeleteStoredWorkflowLiveSuites :exec
DELETE FROM stored_workflow_live_suites WHERE app_id=$1 AND name=$2;

-- name: StoredWorkflowLiveDeployment :one
SELECT id FROM deployments WHERE app_id=$1 AND status='live' AND scope='default'
ORDER BY (traffic_percent>0) DESC,created_at DESC,id DESC LIMIT 1;

-- name: LockStoredWorkflowForLiveSuite :one
SELECT * FROM stored_workflows WHERE app_id=$1 AND name=$2 FOR SHARE;

-- name: CountProtectedStoredWorkflowLiveSuites :one
SELECT count(*) FROM stored_workflow_live_suites AS history WHERE history.app_id=sqlc.arg(app_id)::uuid AND history.name=sqlc.arg(name)::text AND (EXISTS (SELECT 1 FROM stored_workflow_live_baseline_promotions AS pin WHERE pin.app_id=history.app_id AND pin.name=history.name AND pin.suite_id=history.id AND NOT EXISTS (SELECT 1 FROM stored_workflow_live_baseline_retirements AS retired WHERE retired.app_id=pin.app_id AND retired.name=pin.name AND retired.baseline_name=pin.baseline_name) AND pin.revision=(SELECT max(head.revision) FROM stored_workflow_live_baseline_promotions AS head WHERE head.app_id=pin.app_id AND head.name=pin.name AND head.baseline_name=pin.baseline_name)) OR (history.managed AND history.created_at>sqlc.arg(protected_since)::timestamptz));

-- name: GetStoredWorkflowLiveBaselineHead :one
SELECT * FROM stored_workflow_live_baseline_promotions WHERE app_id=$1 AND name=$2 AND baseline_name=$3 ORDER BY revision DESC LIMIT 1;

-- name: GetStoredWorkflowLiveBaselinePromotion :one
SELECT * FROM stored_workflow_live_baseline_promotions WHERE promotion_id=$1;

-- name: GetStoredWorkflowLiveBaselineHistory :many
SELECT * FROM stored_workflow_live_baseline_promotions WHERE app_id=$1 AND name=$2 AND baseline_name=$3 ORDER BY revision DESC LIMIT $4;

-- name: CountStoredWorkflowLiveBaselines :one
SELECT count(DISTINCT head.baseline_name) FROM stored_workflow_live_baseline_promotions AS head WHERE head.app_id=$1 AND head.name=$2 AND NOT EXISTS (SELECT 1 FROM stored_workflow_live_baseline_retirements AS retired WHERE retired.app_id=head.app_id AND retired.name=head.name AND retired.baseline_name=head.baseline_name);

-- name: InsertStoredWorkflowLiveBaselinePromotion :exec
INSERT INTO stored_workflow_live_baseline_promotions(promotion_id,app_id,name,baseline_name,revision,suite_id,receipt,promoted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: PruneStoredWorkflowLiveBaselineHistory :exec
DELETE FROM stored_workflow_live_baseline_promotions WHERE app_id=$1 AND name=$2 AND baseline_name=$3 AND revision <= $4;

-- name: ListStoredWorkflowLiveBaselineHeads :many
SELECT DISTINCT ON (head.baseline_name) head.* FROM stored_workflow_live_baseline_promotions AS head
WHERE head.app_id=$1 AND head.name=$2 AND NOT EXISTS (SELECT 1 FROM stored_workflow_live_baseline_retirements AS retired WHERE retired.app_id=head.app_id AND retired.name=head.name AND retired.baseline_name=head.baseline_name) ORDER BY head.baseline_name,head.revision DESC LIMIT $3;

-- name: GetStoredWorkflowLiveBaselineRetirement :one
SELECT * FROM stored_workflow_live_baseline_retirements WHERE app_id=$1 AND name=$2 AND baseline_name=$3;

-- name: GetStoredWorkflowLiveBaselineRetirementByID :one
SELECT * FROM stored_workflow_live_baseline_retirements WHERE retirement_id=$1;

-- name: InsertStoredWorkflowLiveBaselineRetirement :exec
INSERT INTO stored_workflow_live_baseline_retirements(retirement_id,app_id,name,baseline_name,receipt) VALUES($1,$2,$3,$4,$5);

-- name: CountStoredWorkflowLiveBaselineNames :one
SELECT count(DISTINCT baseline_name) FROM stored_workflow_live_baseline_promotions WHERE app_id=$1 AND name=$2;

-- name: FenceStoredWorkflowLiveBaselineMutation :exec
-- Create a row version so repeatable-read writers waiting on admission locks
-- cannot commit promotion/retirement decisions from an older snapshot.
UPDATE stored_workflows SET updated_at=updated_at WHERE app_id=$1 AND name=$2;
