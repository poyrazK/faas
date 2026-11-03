-- name: FailInterruptedFireNowRequests :execrows
-- A manual cron run claimed (running) but never linked to an invocation, task
-- or operation was interrupted between its claim and its dispatch; nothing
-- else ever settles it. node_id scopes the sweep to one scheduler's apps.
UPDATE cron_fire_now_requests r
   SET status = 'failed', error = sqlc.arg(message)::text, finished_at = now()
  FROM crons c
  JOIN apps a ON a.id = c.app_id
 WHERE c.id = r.cron_id
   AND r.status = 'running'
   AND r.invocation_id IS NULL AND r.task_id IS NULL AND r.operation_id IS NULL
   AND r.requested_at < sqlc.arg(before)::timestamptz
   AND (sqlc.narg(node_id)::text IS NULL OR a.node_id IS NULL OR a.node_id::text = sqlc.narg(node_id));
