-- name: LockApp :one
SELECT id FROM apps WHERE id = sqlc.arg(app_id) AND account_id = sqlc.arg(account_id) AND deleted_at IS NULL FOR SHARE;

-- name: LockCapacity :exec
SELECT pg_advisory_xact_lock(hashtextextended('gregale:data-budget:' || (sqlc.arg(app_id)::text::uuid)::text, 0));

-- name: FindKey :one
SELECT budget_key FROM data_api_shared_budgets WHERE app_id=sqlc.arg(app_id) AND budget_key=sqlc.arg(budget_key) FOR UPDATE;

-- name: ConsumeExisting :one
UPDATE data_api_shared_budgets SET
 requests=CASE WHEN window_end <= sqlc.arg(window_start) THEN 1 ELSE requests+1 END,
 window_start=sqlc.arg(window_start),window_end=sqlc.arg(window_end)
WHERE app_id=sqlc.arg(app_id) AND budget_key=sqlc.arg(budget_key)
 AND (window_end <= sqlc.arg(window_start) OR requests < sqlc.arg(request_limit))
RETURNING requests;

-- name: DatabaseTime :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: Prune :exec
DELETE FROM data_api_shared_budgets AS expired WHERE (expired.app_id,expired.budget_key) IN (
 SELECT b.app_id,b.budget_key FROM data_api_shared_budgets AS b WHERE b.app_id = sqlc.arg(app_id) AND b.window_end <= sqlc.arg(now) LIMIT sqlc.arg(batch_size) FOR UPDATE SKIP LOCKED
) AND expired.window_end <= sqlc.arg(now);

-- name: Consume :one
INSERT INTO data_api_shared_budgets(app_id,budget_key,window_start,window_end,requests)
SELECT sqlc.arg(app_id),sqlc.arg(budget_key),sqlc.arg(window_start),sqlc.arg(window_end),1
WHERE EXISTS (SELECT 1 FROM data_api_shared_budgets AS b WHERE b.app_id=sqlc.arg(app_id) AND b.budget_key=sqlc.arg(budget_key))
 OR (SELECT count(*) FROM data_api_shared_budgets AS b WHERE b.app_id=sqlc.arg(app_id)) < sqlc.arg(max_keys)::bigint
ON CONFLICT(app_id,budget_key) DO UPDATE SET
 requests=CASE WHEN data_api_shared_budgets.window_end <= EXCLUDED.window_start THEN 1 ELSE data_api_shared_budgets.requests+1 END,
 window_start=EXCLUDED.window_start, window_end=EXCLUDED.window_end
WHERE data_api_shared_budgets.window_end <= EXCLUDED.window_start OR data_api_shared_budgets.requests < sqlc.arg(request_limit)
RETURNING requests;
