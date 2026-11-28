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
-- or dead-lettered in a window, through job_runs_failed_finished_idx. A
-- job_name narrows it to the account's jobs of that name (ADR-045
-- amendment 1).
SELECT count(*)::bigint AS total
  FROM job_runs r
 WHERE r.account_id = sqlc.arg(account_id)::uuid
   AND r.aggregate_status IN ('failed', 'dead_letter')
   AND r.finished_at >= sqlc.arg(since)::timestamptz
   AND r.finished_at <= sqlc.arg(until)::timestamptz
   AND (sqlc.narg(job_name)::text IS NULL OR r.job_id IN (
        SELECT j.id FROM jobs j WHERE j.account_id = sqlc.arg(account_id)::uuid AND j.name = sqlc.narg(job_name)::text AND j.status <> 'deleted'));

-- name: ListAccountFailedJobRuns :many
-- The newest failed runs a firing job_failures rule counted, for the
-- notification (ADR-045 amendment 1): job name, run, failed task count and
-- the first failed task's error. A job filter matches only the live job of
-- that name; the name is unique only among live jobs.
SELECT r.id, j.name AS job_name, r.aggregate_status, r.tasks, r.tasks_failed, r.dead_letter_count,
       r.finished_at::timestamptz AS finished_at,
       coalesce((SELECT coalesce(t.error_message, t.error_class, '') FROM job_tasks t
                  WHERE t.run_id = r.id AND t.status <> 'succeeded' AND (t.error_message IS NOT NULL OR t.error_class IS NOT NULL)
                  ORDER BY t.task_index LIMIT 1), '')::text AS first_error
  FROM job_runs r
  JOIN jobs j ON j.id = r.job_id
 WHERE r.account_id = sqlc.arg(account_id)::uuid
   AND r.aggregate_status IN ('failed', 'dead_letter')
   AND r.finished_at >= sqlc.arg(since)::timestamptz
   AND r.finished_at <= sqlc.arg(until)::timestamptz
   AND (sqlc.narg(job_name)::text IS NULL OR (j.name = sqlc.narg(job_name)::text AND j.status <> 'deleted'))
 ORDER BY r.finished_at DESC, r.id
 LIMIT sqlc.arg(row_limit)::integer;

-- name: ListAppBootFailures :many
-- The newest failed boots a firing boot_failures rule counted, for the
-- notification (ADR-045 amendment 1).
SELECT at,
       coalesce(data->>'deployment_id', '')::text AS deployment_id,
       coalesce(data->>'node_id', '')::text AS node_id,
       coalesce(data->>'method', '')::text AS method
  FROM events
 WHERE kind = 'wake.boot_failed'
   AND data->>'app_id' = sqlc.arg(app_id)::text
   AND data->>'reason' = 'vmm_boot_failed'
   AND NOT (data ? 'mirror')
   AND at >= sqlc.arg(since)::timestamptz
   AND at <= sqlc.arg(until)::timestamptz
 ORDER BY at DESC, id DESC
 LIMIT sqlc.arg(row_limit)::integer;

-- name: CountFailedCronInvocations :one
-- A failed_invocations rule narrowed to one cron (ADR-045 amendment 1):
-- that cron's failed and dead-lettered invocations in production, or in the
-- rule's stage when environment_id is set.
SELECT count(*)::bigint FROM invocations
 WHERE account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
   AND cron_id = sqlc.arg(cron_id)::uuid AND source = 'cron'
   AND state IN ('failed', 'dead_letter')
   AND environment_id IS NOT DISTINCT FROM sqlc.narg(environment_id)::uuid
   AND coalesce(completed_at, created_at) >= sqlc.arg(since)::timestamptz;

-- name: ListFailedInvocations :many
-- The newest failed invocations a firing failed_invocations rule counted, for
-- the notification (ADR-045 amendment 1), in production or the rule's stage
-- like the count. Without a source it lists the sources the any count sums.
SELECT id, source, path, cron_id, attempts, coalesce(last_error, '')::text AS last_error,
       coalesce(completed_at, created_at)::timestamptz AS failed_at
  FROM invocations
 WHERE account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
   AND state IN ('failed', 'dead_letter')
   AND environment_id IS NOT DISTINCT FROM sqlc.narg(environment_id)::uuid
   AND (sqlc.narg(source)::text IS NULL AND source IN ('async_invoke', 'queue', 'delayed_task', 'cron', 'inbound_webhook')
        OR source = sqlc.narg(source)::text)
   AND (sqlc.narg(cron_id)::uuid IS NULL OR cron_id = sqlc.narg(cron_id)::uuid)
   AND coalesce(completed_at, created_at) >= sqlc.arg(since)::timestamptz
 ORDER BY coalesce(completed_at, created_at) DESC, id
 LIMIT sqlc.arg(row_limit)::integer;

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
