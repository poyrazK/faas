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

-- name: ReadDeploymentBootEvidence :one
-- ADR-605 amendment 2: a boot_failures rollback judges a completed release on
-- its own boots in [window_start, window_end]. A failed VM boot (not
-- mirror-mode, not schedd's own runtime-state failure) counts only when the
-- same node completed a boot of another deployment in the window, so a node
-- fault, a fleet-wide incident or an app pinned to a broken node never reads
-- as the release failing. successes counts the release's completed boots and
-- restores. The post-deploy window is at most an hour, which bounds both
-- scans; events_boot_failed_app_at_idx and events_kind_at_idx serve them.
SELECT
  (SELECT count(*)
     FROM events f
    WHERE f.kind = 'wake.boot_failed'
      AND f.data->>'app_id' = sqlc.arg(app_id)::text
      AND f.data->>'deployment_id' = sqlc.arg(deployment_id)::text
      AND f.data->>'reason' = 'vmm_boot_failed'
      AND NOT (f.data ? 'mirror')
      AND f.at >= sqlc.arg(window_start)::timestamptz
      AND f.at <= sqlc.arg(window_end)::timestamptz
      AND EXISTS (
        SELECT 1 FROM events c
         WHERE c.kind = 'wake.boot_completed'
           AND c.at >= sqlc.arg(window_start)::timestamptz
           AND c.at <= sqlc.arg(window_end)::timestamptz
           AND c.data->>'node_id' = f.data->>'node_id'
           AND c.data->>'deployment_id' IS DISTINCT FROM sqlc.arg(deployment_id)::text))::bigint AS failures,
  (SELECT count(*)
     FROM events s
    WHERE s.kind = 'wake.boot_completed'
      AND s.data->>'app_id' = sqlc.arg(app_id)::text
      AND s.data->>'deployment_id' = sqlc.arg(deployment_id)::text
      AND s.at >= sqlc.arg(window_start)::timestamptz
      AND s.at <= sqlc.arg(window_end)::timestamptz)::bigint AS successes;
