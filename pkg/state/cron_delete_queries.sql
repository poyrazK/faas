-- name: ListPendingCronInvocationIDs :many
-- Runs of a cron that have not started: queued, or waiting to retry. Deleting
-- the cron cancels them so a deleted cron never calls the app again. The app
-- check keeps a delete addressed to the wrong app from touching these runs.
select i.id::text from invocations i
  join crons c on c.id = i.cron_id
 where i.cron_id = sqlc.arg(cron_id)::uuid
   and c.app_id = sqlc.arg(app_id)::uuid
   and i.state = 'pending'
 order by i.created_at, i.id
 limit sqlc.arg(row_limit)::int;
