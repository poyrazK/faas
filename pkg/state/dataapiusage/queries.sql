-- name: LockUsageApp :one
-- The token's app must still belong to its account; the row lock orders a
-- report against app deletion.
SELECT a.id::text AS id FROM apps a
 WHERE a.id=sqlc.arg(app_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.deleted_at IS NULL
 FOR KEY SHARE;

-- name: ClaimUsageReport :execrows
-- Zero rows: the report id was already applied, so its deltas are not added again.
INSERT INTO data_api_usage_reports (app_id,report_id)
 VALUES (sqlc.arg(app_id)::text::uuid,sqlc.arg(report_id)::text)
 ON CONFLICT (app_id,report_id) DO NOTHING;

-- name: AddUsageHour :exec
INSERT INTO data_api_usage_hourly (app_id,hour,user_requests,anonymous_requests,server_requests,rejected_requests,error_requests,capped_requests,rows_returned,response_bytes)
 VALUES (sqlc.arg(app_id)::text::uuid,sqlc.arg(hour)::timestamptz,sqlc.arg(user_requests)::bigint,sqlc.arg(anonymous_requests)::bigint,sqlc.arg(server_requests)::bigint,
  sqlc.arg(rejected_requests)::bigint,sqlc.arg(error_requests)::bigint,sqlc.arg(capped_requests)::bigint,sqlc.arg(rows_returned)::bigint,sqlc.arg(response_bytes)::bigint)
 ON CONFLICT (app_id,hour) DO UPDATE SET
  user_requests=data_api_usage_hourly.user_requests+EXCLUDED.user_requests,
  anonymous_requests=data_api_usage_hourly.anonymous_requests+EXCLUDED.anonymous_requests,
  server_requests=data_api_usage_hourly.server_requests+EXCLUDED.server_requests,
  rejected_requests=data_api_usage_hourly.rejected_requests+EXCLUDED.rejected_requests,
  error_requests=data_api_usage_hourly.error_requests+EXCLUDED.error_requests,
  capped_requests=data_api_usage_hourly.capped_requests+EXCLUDED.capped_requests,
  rows_returned=data_api_usage_hourly.rows_returned+EXCLUDED.rows_returned,
  response_bytes=data_api_usage_hourly.response_bytes+EXCLUDED.response_bytes,
  updated_at=now();

-- name: UsageTotals :one
-- Cap-relevant totals for a window: admitted requests, all and anonymous.
SELECT COALESCE(sum(user_requests+anonymous_requests+server_requests),0)::bigint AS requests,
 COALESCE(sum(anonymous_requests),0)::bigint AS anonymous_requests
 FROM data_api_usage_hourly
 WHERE app_id=sqlc.arg(app_id)::text::uuid AND hour>=sqlc.arg(from_hour)::timestamptz AND hour<sqlc.arg(to_hour)::timestamptz;

-- name: ListUsageHours :many
SELECT hour,user_requests,anonymous_requests,server_requests,rejected_requests,error_requests,capped_requests,rows_returned,response_bytes
 FROM data_api_usage_hourly
 WHERE app_id=sqlc.arg(app_id)::text::uuid AND hour>=sqlc.arg(from_hour)::timestamptz AND hour<sqlc.arg(to_hour)::timestamptz
 ORDER BY hour;

-- name: PruneUsageReports :execrows
DELETE FROM data_api_usage_reports WHERE ctid IN (
 SELECT r.ctid FROM data_api_usage_reports r
  WHERE r.app_id=sqlc.arg(app_id)::text::uuid AND r.received_at<sqlc.arg(before)::timestamptz
  LIMIT sqlc.arg(row_limit)::integer);

-- name: PruneUsageHours :execrows
DELETE FROM data_api_usage_hourly WHERE ctid IN (
 SELECT h.ctid FROM data_api_usage_hourly h
  WHERE h.app_id=sqlc.arg(app_id)::text::uuid AND h.hour<sqlc.arg(before)::timestamptz
  LIMIT sqlc.arg(row_limit)::integer);

-- name: UpsertAccessConfig :exec
INSERT INTO data_api_access_configs (app_id,config,reported_at)
 VALUES (sqlc.arg(app_id)::text::uuid,sqlc.arg(config)::jsonb,now())
 ON CONFLICT (app_id) DO UPDATE SET config=EXCLUDED.config, reported_at=EXCLUDED.reported_at;

-- name: GetAccessConfig :one
SELECT config,reported_at FROM data_api_access_configs WHERE app_id=sqlc.arg(app_id)::text::uuid;

-- name: DeleteAccessConfig :execrows
DELETE FROM data_api_access_configs c USING apps a
 WHERE c.app_id=sqlc.arg(app_id)::text::uuid AND a.id=c.app_id AND a.account_id=sqlc.arg(account_id)::text::uuid;
