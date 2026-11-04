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
-- Retention: pauses that started before cutoff, at most batch_limit rows per
-- call. An open one that old belongs to a VM long gone; its node simply never
-- reported again.
DELETE FROM instance_quiet_pauses
 WHERE ctid IN (SELECT ctid FROM instance_quiet_pauses
                 WHERE started_at < sqlc.arg(cutoff)::timestamptz
                 LIMIT sqlc.arg(batch_limit)::integer);
