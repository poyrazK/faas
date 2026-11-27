-- name: CountAppBootFailures :one
-- The boot_failures alert metric: the app's failed VM boots in a window.
-- record_runtime_failed (schedd could not publish runtime state) is a
-- platform cause and mirror-mode boots belong to a candidate deployment, not
-- the live app, so neither counts. events_boot_failed_app_at_idx keys the
-- scan by app so a fleet-wide incident does not widen it.
SELECT count(*)::bigint AS total
  FROM events
 WHERE kind = 'wake.boot_failed'
   AND data->>'app_id' = sqlc.arg(app_id)::text
   AND at >= sqlc.arg(since)::timestamptz
   AND at <= sqlc.arg(until)::timestamptz
   AND data->>'reason' = 'vmm_boot_failed'
   AND NOT (data ? 'mirror');

-- name: CountAccountFailedJobRuns :one
-- The job_failures alert metric: the account's job runs that finished failed
-- or dead-lettered in a window, through job_runs_failed_finished_idx.
SELECT count(*)::bigint AS total
  FROM job_runs
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND aggregate_status IN ('failed', 'dead_letter')
   AND finished_at >= sqlc.arg(since)::timestamptz
   AND finished_at <= sqlc.arg(until)::timestamptz;
