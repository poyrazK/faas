-- ADR-20261023171905: the quiet pause ledger. schedd records the pauses vmmd
-- reports; meterd subtracts the paused seconds the ledger proves.

-- name: RecordQuietPauses :exec
-- One node report in one statement. A pause only ever gains a later end, so a
-- repeated open report never reopens or shortens a closed pause, and the
-- node's watermark only moves forward. The caller sends each pause once.
WITH pauses AS (
    INSERT INTO instance_quiet_pauses (instance_id, node_id, started_at, ended_at)
    SELECT p.instance_id, sqlc.arg(node_id)::uuid, p.started_at, p.ended_at
      FROM (SELECT unnest(sqlc.arg(instance_ids)::uuid[]) AS instance_id,
                   unnest(sqlc.arg(started_ats)::timestamptz[]) AS started_at,
                   unnest(sqlc.arg(ended_ats)::timestamptz[]) AS ended_at) p
    ON CONFLICT (instance_id, started_at) DO UPDATE SET ended_at = EXCLUDED.ended_at
     WHERE EXCLUDED.ended_at IS NOT NULL
       AND (instance_quiet_pauses.ended_at IS NULL OR EXCLUDED.ended_at > instance_quiet_pauses.ended_at)
)
INSERT INTO quiet_pause_watermarks (node_id, covered_until)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(covered_until)::timestamptz)
ON CONFLICT (node_id) DO UPDATE
   SET covered_until = GREATEST(quiet_pause_watermarks.covered_until, EXCLUDED.covered_until),
       updated_at = now();

-- name: OpenQuietPauses :many
-- A node's open pauses, which schedd reconciles against the node's next report
-- after it restarts.
SELECT instance_id::text AS instance_id, started_at
  FROM instance_quiet_pauses
 WHERE node_id = sqlc.arg(node_id)::uuid AND ended_at IS NULL;

-- name: QuietPausedSeconds :many
-- Each instance's proven quiet-paused seconds in [window_start, window_end).
-- A closed pause is exact. An open pause counts only up to its node's
-- watermark, and not at all without one, so a lagging node bills more, never
-- less. unproven marks an instance with an open pause the watermark has not
-- covered to the window's end. The total is floored to whole seconds.
SELECT p.instance_id::text AS instance_id,
       floor(sum(greatest(0, extract(epoch FROM
           least(coalesce(p.ended_at, w.covered_until, p.started_at), sqlc.arg(window_end)::timestamptz)
         - greatest(p.started_at, sqlc.arg(window_start)::timestamptz)))))::bigint AS paused_seconds,
       bool_or(p.ended_at IS NULL
               AND coalesce(w.covered_until < sqlc.arg(window_end)::timestamptz, true))::boolean AS unproven
  FROM instance_quiet_pauses p
  LEFT JOIN quiet_pause_watermarks w ON w.node_id = p.node_id
 WHERE p.started_at < sqlc.arg(window_end)::timestamptz
   AND (p.ended_at IS NULL OR p.ended_at > sqlc.arg(window_start)::timestamptz)
 GROUP BY p.instance_id;

-- name: DeleteQuietPausesBefore :execrows
-- Retention, at most batch_limit rows per call: pauses that ended before
-- cutoff, and open ones that started before it on a node whose watermark has
-- not reached it, a VM long gone whose node simply never reported again. An
-- open pause on a reporting node is kept however old: a floor-held VM with no
-- traffic stays quiet-paused for days, and its node sends the pause once.
DELETE FROM instance_quiet_pauses
 WHERE ctid IN (SELECT p.ctid FROM instance_quiet_pauses p
                 WHERE p.ended_at < sqlc.arg(cutoff)::timestamptz
                    OR (p.ended_at IS NULL
                        AND p.started_at < sqlc.arg(cutoff)::timestamptz
                        AND NOT EXISTS (SELECT 1 FROM quiet_pause_watermarks w
                                         WHERE w.node_id = p.node_id
                                           AND w.covered_until >= sqlc.arg(cutoff)::timestamptz))
                 LIMIT sqlc.arg(batch_limit)::integer);

-- name: RecordQuietPausedUsage :exec
-- The MB-seconds of one instance-minute meterd did not bill because the
-- ledger proved the VM quiet-paused (rollout step 3). As with mb_seconds the
-- first positive value wins, so a caught-up minute never changes what the
-- customer already saw. A minute paused throughout bills nothing and writes
-- its row here, with mb_seconds 0.
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, paused_mb_seconds)
values (sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(instance_id)::uuid,
        sqlc.arg(minute)::timestamptz, 0, sqlc.arg(paused_mb_seconds)::bigint)
on conflict (instance_id, minute) do update
   set paused_mb_seconds = case when usage_minutes.paused_mb_seconds = 0 and EXCLUDED.paused_mb_seconds > 0
                                then EXCLUDED.paused_mb_seconds else usage_minutes.paused_mb_seconds end;

-- name: QuietPausedMBSecondsByMonth :one
-- An account's quiet-paused MB-seconds for one UTC month, from the
-- (account_id, minute) index.
select coalesce(sum(paused_mb_seconds), 0)::bigint as paused_mb_seconds
  from usage_minutes
 where account_id = sqlc.arg(account_id)::uuid
   and minute >= sqlc.arg(month_start)::timestamptz
   and minute <  sqlc.arg(month_end)::timestamptz;
