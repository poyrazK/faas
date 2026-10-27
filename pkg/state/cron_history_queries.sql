-- Cron history retention (api.CronHistoryRetention). Both deletes walk the
-- live crons and probe each cron's own history index, so a sweep never scans
-- the whole table. Rows of deleted crons are already gone: both tables
-- cascade from crons.

-- name: PruneCronScheduleOccurrences :execrows
-- Settled schedule decisions scheduled before before_at, oldest first.
-- Pending, queued, running, waiting_replacement and uncertain decisions are
-- kept whatever their age. References from invocations, app tasks, job runs
-- and blocking occurrences are ON DELETE SET NULL.
DELETE FROM schedule_occurrences WHERE id IN (
  SELECT o.id FROM crons c
  CROSS JOIN LATERAL (
    SELECT so.id FROM schedule_occurrences so
     WHERE so.cron_id = c.id
       AND so.scheduled_for < sqlc.arg(before_at)::timestamptz
       AND so.status IN ('succeeded', 'failed', 'cancelled', 'skipped_overlap', 'missed_deadline', 'coalesced')
     ORDER BY so.scheduled_for, so.id
     LIMIT sqlc.arg(batch_limit)::int) o
  LIMIT sqlc.arg(batch_limit)::int);

-- name: PruneCronFireNowRequests :execrows
-- Manual-run requests made before before_at, whatever their status: a
-- request that old can no longer start a run, and its outcome lives on the
-- run itself.
DELETE FROM cron_fire_now_requests WHERE id IN (
  SELECT r.id FROM crons c
  CROSS JOIN LATERAL (
    SELECT f.id FROM cron_fire_now_requests f
     WHERE f.cron_id = c.id
       AND f.requested_at < sqlc.arg(before_at)::timestamptz
     ORDER BY f.requested_at, f.id
     LIMIT sqlc.arg(batch_limit)::int) r
  LIMIT sqlc.arg(batch_limit)::int);
