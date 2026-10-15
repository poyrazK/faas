-- name: GetAutomationForLiveVerification :one
SELECT * FROM workflow_automation_definitions WHERE app_id=$1 AND name=$2;

-- name: InsertAutomationLiveVerification :one
INSERT INTO workflow_automation_live_verifications
(run_id,app_id,name,case_name,automation_version,published_version,deployment_id,inputs_hash,live_test,idempotency_key,request_fingerprint)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;

-- name: GetAutomationLiveVerificationByKey :one
SELECT * FROM workflow_automation_live_verifications WHERE app_id=$1 AND name=$2 AND idempotency_key=$3;

-- name: GetAutomationLiveVerification :one
SELECT * FROM workflow_automation_live_verifications WHERE app_id=$1 AND name=$2 AND run_id=$3;

-- name: ListAutomationLiveVerifications :many
SELECT * FROM workflow_automation_live_verifications WHERE app_id=$1 AND name=$2
ORDER BY created_at DESC,run_id DESC LIMIT $3;

-- name: AutomationLiveRunState :one
SELECT status, resume_count, updated_at, cancelled_at FROM workflow_runs WHERE id=$1;

-- name: LockAutomationLiveRunState :one
SELECT status, resume_count, updated_at, cancelled_at FROM workflow_runs WHERE id=$1 FOR UPDATE;

-- name: AutomationLiveStep :many
SELECT s.status,s.output,s.attempt FROM workflow_steps s
WHERE s.run_id=sqlc.arg(run_id)::uuid
AND ((sqlc.arg(loop_name)::text='' AND s.step_name=sqlc.arg(step_name)::text AND s.foreach_parent IS NULL AND s.foreach_index IS NULL)
 OR (sqlc.arg(loop_name)::text<>'' AND s.foreach_parent=sqlc.arg(loop_name)::text AND s.foreach_index=sqlc.narg(item_index)::integer))
LIMIT 2;

-- name: SaveAutomationLiveReport :execrows
UPDATE workflow_automation_live_verifications SET report=$4
WHERE app_id=$1 AND name=$2 AND run_id=$3;

-- name: DeleteAutomationLiveVerifications :exec
DELETE FROM workflow_automation_live_verifications WHERE app_id=$1 AND name=$2;

-- name: InsertAutomationLiveSuite :one
INSERT INTO workflow_automation_live_suites(id,app_id,name,automation_version,published_version,deployment_id,cases,managed)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING RETURNING *;

-- name: GetAutomationLiveSuite :one
SELECT * FROM workflow_automation_live_suites WHERE app_id=$1 AND name=$2 AND id=$3;

-- name: UpdateAutomationLiveSuite :one
UPDATE workflow_automation_live_suites SET cases=$4,updated_at=now() WHERE app_id=$1 AND name=$2 AND id=$3 RETURNING *;

-- name: ListAutomationLiveSuites :many
SELECT * FROM workflow_automation_live_suites WHERE app_id=$1 AND name=$2 ORDER BY created_at DESC,id DESC LIMIT $3;

-- name: PruneAutomationLiveSuites :exec
DELETE FROM workflow_automation_live_suites AS suite WHERE suite.app_id=sqlc.arg(app_id)::uuid AND suite.name=sqlc.arg(name)::text AND suite.id IN (
 SELECT history.id FROM workflow_automation_live_suites AS history WHERE history.app_id=sqlc.arg(app_id)::uuid AND history.name=sqlc.arg(name)::text
 ORDER BY (history.managed AND history.created_at>sqlc.arg(protected_since)::timestamptz) DESC,history.created_at DESC,history.id DESC OFFSET sqlc.arg(retained)::integer
);

-- name: DeleteAutomationLiveSuites :exec
DELETE FROM workflow_automation_live_suites WHERE app_id=$1 AND name=$2;

-- name: AutomationLiveDeployment :one
SELECT id FROM deployments WHERE app_id=$1 AND status='live' AND scope='default'
ORDER BY (traffic_percent>0) DESC,created_at DESC,id DESC LIMIT 1;

-- name: LockAutomationForLiveSuite :one
SELECT * FROM workflow_automation_definitions WHERE app_id=$1 AND name=$2 FOR SHARE;

-- name: CountProtectedAutomationLiveSuites :one
SELECT count(*) FROM workflow_automation_live_suites WHERE app_id=sqlc.arg(app_id)::uuid AND name=sqlc.arg(name)::text AND managed AND created_at>sqlc.arg(protected_since)::timestamptz;
