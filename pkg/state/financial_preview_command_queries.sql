-- name: FinancialPreviewCommandRegister :execrows
INSERT INTO financial_preview_command_runtimes(task_id, lease_token, account_id, app_id, deployment_id, node_id, environment_id, phase, ram_mb, cpu_millicores, recorded_at, prepare_until)
SELECT t.id, t.lease_token, t.account_id, t.app_id, t.deployment_id, sqlc.arg(node_id)::uuid,
  sqlc.narg(environment_id)::uuid, 'preparing', sqlc.arg(ram_mb)::integer, sqlc.arg(cpu_millicores)::integer, sqlc.arg(recorded_at)::timestamptz, sqlc.arg(prepare_until)::timestamptz
FROM app_tasks t JOIN apps a ON a.id=t.app_id AND a.account_id=t.account_id
WHERE t.id=sqlc.arg(task_id)::uuid AND t.account_id=sqlc.arg(account_id)::uuid AND t.app_id=sqlc.arg(app_id)::uuid
 AND t.deployment_id=sqlc.arg(deployment_id)::uuid AND t.lease_token=sqlc.arg(lease_token)::uuid
 AND t.status='restoring' AND t.lease_expires_at>sqlc.arg(recorded_at)::timestamptz
 AND a.preview_of_slug IS NOT NULL AND a.preview_of_slug!=''
 AND NOT financial_app_command_held(t.app_id,t.deployment_id,sqlc.arg(recorded_at)::timestamptz)
ON CONFLICT (task_id,lease_token) DO NOTHING;

-- name: FinancialPreviewCommandHeld :one
SELECT financial_app_command_held(sqlc.arg(app_id)::uuid,sqlc.arg(deployment_id)::uuid,sqlc.arg(observed_at)::timestamptz)::boolean AS held;

-- name: FinancialPreviewCommandActivate :execrows
UPDATE financial_preview_command_runtimes SET phase='active'
WHERE task_id=sqlc.arg(task_id)::uuid AND lease_token=sqlc.arg(lease_token)::uuid AND phase='preparing';

-- name: FinancialPreviewCommandRetire :execrows
UPDATE financial_preview_command_runtimes SET phase='retired',retired_at=sqlc.arg(retired_at)::timestamptz
WHERE task_id=sqlc.arg(task_id)::uuid AND lease_token=sqlc.arg(lease_token)::uuid;

-- name: FinancialPreviewCommandList :many
SELECT * FROM financial_preview_command_runtimes
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND phase!='retired'
ORDER BY task_id LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialPreviewCommandUnowned :many
SELECT t.id,t.deployment_id FROM app_tasks t
WHERE t.account_id=sqlc.arg(account_id)::uuid AND t.app_id=sqlc.arg(app_id)::uuid AND t.status IN ('restoring','running')
 AND NOT EXISTS(SELECT 1 FROM financial_preview_command_runtimes r WHERE r.task_id=t.id)
ORDER BY t.id LIMIT sqlc.arg(page_size)::integer;
