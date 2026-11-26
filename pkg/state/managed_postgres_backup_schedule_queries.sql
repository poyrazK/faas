-- name: GetNativePostgresBackupSchedule :one
SELECT * FROM native_postgres_backup_schedules
WHERE database_id = @database_id AND owner_id = @owner_id AND org_id = @org_id;

-- name: SaveNativePostgresBackupSchedule :one
INSERT INTO native_postgres_backup_schedules
(database_id, owner_id, org_id, bucket_account_id, bucket_app_id, bucket_id, utc_minute, next_run_at, retry_at)
VALUES (@database_id, @owner_id, @org_id, @bucket_account_id, @bucket_app_id, @bucket_id, @utc_minute, @next_run_at, @next_run_at)
ON CONFLICT (database_id) DO UPDATE SET
    bucket_account_id = EXCLUDED.bucket_account_id,
    bucket_app_id = EXCLUDED.bucket_app_id,
    bucket_id = EXCLUDED.bucket_id,
    utc_minute = EXCLUDED.utc_minute,
    next_run_at = EXCLUDED.next_run_at,
    retry_at = EXCLUDED.retry_at,
    run_id = NULL, run_deadline = NULL, export_queued = false, lease_token = NULL, lease_until = NULL,
    last_success_at = CASE WHEN native_postgres_backup_schedules.bucket_id = EXCLUDED.bucket_id THEN native_postgres_backup_schedules.last_success_at END,
    last_archive_key = CASE WHEN native_postgres_backup_schedules.bucket_id = EXCLUDED.bucket_id THEN native_postgres_backup_schedules.last_archive_key END,
    last_error_code = NULL
WHERE native_postgres_backup_schedules.owner_id = EXCLUDED.owner_id
  AND native_postgres_backup_schedules.org_id = EXCLUDED.org_id
  AND (native_postgres_backup_schedules.lease_until IS NULL OR native_postgres_backup_schedules.lease_until <= clock_timestamp())
  AND (native_postgres_backup_schedules.bucket_id IS DISTINCT FROM EXCLUDED.bucket_id
       OR native_postgres_backup_schedules.bucket_account_id IS DISTINCT FROM EXCLUDED.bucket_account_id
       OR native_postgres_backup_schedules.bucket_app_id IS DISTINCT FROM EXCLUDED.bucket_app_id
       OR native_postgres_backup_schedules.utc_minute IS DISTINCT FROM EXCLUDED.utc_minute)
RETURNING *;

-- name: DeleteNativePostgresBackupSchedule :execrows
DELETE FROM native_postgres_backup_schedules
WHERE database_id = @database_id AND owner_id = @owner_id AND org_id = @org_id
  AND (lease_until IS NULL OR lease_until <= clock_timestamp());

-- name: ClaimNativePostgresBackupSchedule :one
UPDATE native_postgres_backup_schedules SET
    lease_token = @lease_token,
    lease_until = clock_timestamp() + interval '17 minutes',
    run_id = COALESCE(run_id, @run_id),
    run_deadline = COALESCE(run_deadline, clock_timestamp() + interval '3 hours')
WHERE database_id = (
    SELECT s.database_id FROM native_postgres_backup_schedules s
    JOIN managed_postgres_databases d ON d.id = s.database_id
    WHERE d.state = 'ready' AND d.account_id::text = s.owner_id AND coalesce(d.org_id::text, '') = s.org_id
      AND s.next_run_at <= clock_timestamp() AND s.retry_at <= clock_timestamp()
      AND (s.lease_until IS NULL OR s.lease_until <= clock_timestamp())
    ORDER BY s.retry_at, s.database_id LIMIT 1 FOR UPDATE OF s SKIP LOCKED
)
RETURNING *;

-- name: RetryNativePostgresBackupSchedule :execrows
UPDATE native_postgres_backup_schedules SET
    lease_token = NULL, lease_until = NULL,
    retry_at = clock_timestamp() + make_interval(secs => @retry_seconds::double precision),
    export_queued = @export_queued,
    last_error_code = NULLIF(@error_code::text, '')
WHERE database_id = @database_id AND lease_token = @lease_token AND lease_until > clock_timestamp();

-- name: FinishNativePostgresBackupSchedule :execrows
UPDATE native_postgres_backup_schedules SET
    lease_token = NULL, lease_until = NULL, run_id = NULL, run_deadline = NULL, export_queued = false,
    next_run_at = @next_run_at, retry_at = @next_run_at,
    last_success_at = COALESCE(sqlc.narg(success_at), last_success_at),
    last_archive_key = COALESCE(sqlc.narg(archive_key), last_archive_key),
    last_error_code = NULLIF(@error_code::text, '')
WHERE database_id = @database_id AND lease_token = @lease_token AND lease_until > clock_timestamp();
