-- name: RunControlNow :one
SELECT clock_timestamp()::timestamptz;
-- name: RunControlWriterProtocol :exec
SELECT set_config('faas.run_controls_protocol','1',true);
-- name: RunBudgetGet :one
SELECT * FROM execution_run_budgets WHERE account_id=$1 AND kind=$2 AND ref=$3;
-- name: RunBudgetPut :one
INSERT INTO execution_run_budgets(account_id,kind,ref,revision,enabled,max_concurrent,max_invocations,max_compute_mb_ms)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(account_id,kind,ref) DO UPDATE SET revision=EXCLUDED.revision,enabled=EXCLUDED.enabled,max_concurrent=EXCLUDED.max_concurrent,max_invocations=EXCLUDED.max_invocations,max_compute_mb_ms=EXCLUDED.max_compute_mb_ms RETURNING *;
-- name: RunCustomerCount :one
SELECT count(*) FROM execution_run_budgets WHERE account_id=$1 AND kind='customer';
-- name: RunBindingCount :one
SELECT count(*) FROM execution_run_bindings WHERE account_id=$1;
-- name: RunControlKeyFamily :one
SELECT runs_principal_id FROM api_keys WHERE account_id=$1 AND id=$2;
-- name: RunBindingGet :one
SELECT * FROM execution_run_bindings WHERE account_id=$1 AND subject_kind=$2 AND subject_id=$3;
-- name: RunBindingPut :one
INSERT INTO execution_run_bindings(account_id,subject_kind,subject_id,customer_ref,agent_ref,revision,enabled)
VALUES($1,$2,$3,$4,$5,$6,true) ON CONFLICT(account_id,subject_kind,subject_id) DO UPDATE SET customer_ref=EXCLUDED.customer_ref,agent_ref=EXCLUDED.agent_ref,revision=EXCLUDED.revision,enabled=true RETURNING *;
-- name: RunBindingDisable :exec
UPDATE execution_run_bindings SET revision=revision+1,enabled=false WHERE account_id=$1 AND subject_kind=$2 AND subject_id=$3;
-- name: RunBudgetUsage :one
SELECT count(*) FILTER (WHERE u.execution_id IS NULL)::bigint AS active,
 count(*) FILTER (WHERE r.admitted_at>=sqlc.arg(day_start)::timestamptz AND r.admitted_at<sqlc.arg(day_end)::timestamptz)::bigint AS invocations,
 COALESCE(sum(LEAST(r.reserved_mb_ms::numeric,(r.reserved_mb_ms::numeric/e.timeout_ms)*u.wall_time_ms)) FILTER (WHERE u.execution_id IS NOT NULL AND r.admitted_at>=sqlc.arg(day_start)::timestamptz AND r.admitted_at<sqlc.arg(day_end)::timestamptz),0)::bigint AS used_mb_ms,
 COALESCE(sum(r.reserved_mb_ms) FILTER (WHERE u.execution_id IS NULL AND r.admitted_at>=sqlc.arg(day_start)::timestamptz AND r.admitted_at<sqlc.arg(day_end)::timestamptz),0)::bigint AS reserved_mb_ms
FROM execution_run_reservations r JOIN executions e ON e.id=r.execution_id
LEFT JOIN execution_usage_ledger u ON u.execution_id=r.execution_id
WHERE r.account_id=sqlc.arg(account_id) AND ((sqlc.arg(kind)::text='customer' AND r.customer_ref=sqlc.arg(ref)::text) OR (sqlc.arg(kind)::text='task' AND r.task_name=sqlc.arg(ref)::text));
-- name: RunReservationInsert :exec
INSERT INTO execution_run_reservations(execution_id,account_id,customer_ref,agent_ref,task_name,reserved_mb_ms,admitted_at) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: RunAttributionGet :one
SELECT customer_ref,agent_ref FROM execution_run_reservations WHERE account_id=$1 AND execution_id=$2;
-- name: RunAttributionsGet :many
SELECT execution_id,customer_ref,agent_ref FROM execution_run_reservations WHERE account_id=$1 AND execution_id=ANY(sqlc.arg(execution_ids)::uuid[]);
-- name: RunBudgetAlertInsert :execrows
INSERT INTO execution_run_budget_alerts(event_id,account_id,kind,ref,day,metric,threshold_percent,maximum,observed,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING;
-- name: RunBudgetAlertsGet :many
SELECT * FROM execution_run_budget_alerts WHERE account_id=$1 AND kind=$2 AND ref=$3 AND day=$4 ORDER BY event_id;
-- name: RunBudgetAlertDeliveries :exec
INSERT INTO app_webhook_deliveries(webhook_id,app_id,account_id,event,payload,source_event_id)
SELECT w.id,NULL,sqlc.arg(account_id),'execution.budget.threshold',sqlc.arg(payload)::jsonb,sqlc.arg(event_id)::uuid FROM app_webhooks w
WHERE w.account_id=sqlc.arg(account_id) AND w.scope='account' AND w.enabled AND 'execution.budget.threshold'=ANY(w.event_filter)
ON CONFLICT(source_event_id,webhook_id) WHERE source_event_id IS NOT NULL DO NOTHING;
-- name: RunHistoryList :many
SELECT e.id,e.runtime,e.status,e.created_at,e.started_at,e.finished_at,e.wall_time_ms,e.cpu_time_ms,e.peak_memory_mb,
 COALESCE(e.failure_code,'')::text AS failure_code, COALESCE(e.result_validation->>'status','')::text AS result_validation_status,
 COALESCE(r.customer_ref,'')::text AS customer_ref,COALESCE(r.agent_ref,'')::text AS agent_ref,
 v.id AS task_id,COALESCE(v.name,'')::text AS task_name,COALESCE(v.version,0)::integer AS task_version,COALESCE(v.digest,'')::text AS task_digest
FROM executions e LEFT JOIN execution_run_reservations r ON r.execution_id=e.id
LEFT JOIN execution_task_invocations i ON i.execution_id=e.id LEFT JOIN execution_task_versions v ON v.id=i.version_id
WHERE e.account_id=sqlc.arg(account_id) AND (sqlc.arg(wide)::boolean OR e.runs_principal_id=sqlc.narg(principal_id)::uuid)
AND (sqlc.arg(task_name)::text='' OR v.name=sqlc.arg(task_name)::text)
AND (sqlc.arg(task_version)::integer=0 OR v.version=sqlc.arg(task_version)::integer)
AND (sqlc.arg(customer_ref)::text='' OR r.customer_ref=sqlc.arg(customer_ref)::text)
AND (sqlc.arg(agent_ref)::text='' OR r.agent_ref=sqlc.arg(agent_ref)::text)
AND (sqlc.arg(status)::text='' OR e.status=sqlc.arg(status)::text)
AND (sqlc.narg(since)::timestamptz IS NULL OR e.created_at>=sqlc.narg(since)::timestamptz)
AND (sqlc.narg(until)::timestamptz IS NULL OR e.created_at<sqlc.narg(until)::timestamptz)
AND (sqlc.narg(before_at)::timestamptz IS NULL OR (e.created_at,e.id)<(sqlc.narg(before_at)::timestamptz,sqlc.narg(before_id)::uuid))
ORDER BY e.created_at DESC,e.id DESC LIMIT sqlc.arg(page_limit)::integer;
